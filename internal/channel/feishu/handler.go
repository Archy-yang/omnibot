package feishu

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	"omnibot/pkg/logger"
)

// InboundMessage 飞书入站消息的中性表示——SDK 事件经 Channel 解析后得到此结构,
// 传给 HandleInbound 走 pipeline。把 SDK 类型隔离在 channel.go 内,handler 完全可测。
type InboundMessage struct {
	OpenID   string // 发送者 open_id
	Text     string // 文本消息内容(非 text 消息上层已过滤)
	MsgID    string // 飞书 message_id,用作 SaveUserMessage 的去重键
	ChatType string // "p2p" / "group" / ...,v1.6 只处理 p2p
}

// MessageHandler 飞书消息处理器。所有依赖走接口,无 SDK 依赖,完全可单测。
type MessageHandler struct {
	userService      UserService
	messageService   MessageService
	agentService     AgentService
	llmConfigService LLMConfigService
	sender           Sender
}

// NewMessageHandler 创建飞书消息处理器。
func NewMessageHandler(
	user UserService,
	msg MessageService,
	agent AgentService,
	cfg LLMConfigService,
	sender Sender,
) *MessageHandler {
	return &MessageHandler{
		userService:      user,
		messageService:   msg,
		agentService:     agent,
		llmConfigService: cfg,
		sender:           sender,
	}
}

// fallbackReply 是 agent 执行失败时给用户的兜底文案——和 web 端「服务暂时不可用」对齐,
// 避免无反馈;不透传内部错误细节(安全红线)。
const fallbackReply = "服务暂时不可用,请稍后再试"

// HandleInbound 飞书消息 pipeline。流程严格镜像 web/HandleSendMessageAgent:
//
//	GetOrCreateByChannel("feishu",openID) → SaveUserMessage(去重) → BuildContextMessages →
//	选 LLM(自定义优先) → AgentService.Run → records→steps 落 agent_steps + assistant 消息
//	→ Sender.SendText(最终文本)
//
// 返回的 error 是「值得让 SDK 上层重试或关连接」的硬错误;预期事件(重复消息/agent 失败/
// 发送失败)在内部消化并返回 nil。空文本、群聊也返回 nil(过滤)。
func (h *MessageHandler) HandleInbound(ctx context.Context, in InboundMessage) error {
	// v1.6 仅处理单聊;群聊留后续(@ 解析、群成员校验等复杂度独立做)
	if in.ChatType != "p2p" {
		return nil
	}
	// 空文本不触发 agent
	if strings.TrimSpace(in.Text) == "" {
		return nil
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("feishu", in.OpenID)
	if err != nil {
		logger.ErrorWithFields("feishu: get or create user failed",
			zap.String("open_id", in.OpenID),
			zap.Error(err),
		)
		return err
	}
	userID := user.GetID()

	// 保存用户消息(飞书 message_id 做幂等)
	if err := h.messageService.SaveUserMessage(ctx, userID, in.Text, in.MsgID); err != nil {
		if errors.Is(err, chatsvc.ErrDuplicateMessage) {
			// SDK 可能因网络波动重投同一事件;静默丢弃,不重复回复
			logger.InfoWithFields("feishu: duplicate message ignored",
				zap.Int64("user_id", userID),
				zap.String("msg_id", in.MsgID),
			)
			return nil
		}
		// 其他错误仅记录,继续走 agent(宁可重复也不丢消息)
		logger.WarnWithFields("feishu: save user message failed, continuing",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	ctxMessages, err := h.messageService.BuildContextMessages(ctx, userID, in.Text)
	if err != nil {
		ctxMessages = []llm.ChatMessage{{Role: "user", Content: in.Text}}
	}

	// 选 LLM:用户自定义配置优先(与 web 同步端点完全一致)
	var activeLLMClient agentpkg.LLMClient
	stepModel := ""
	userConfig, hasCustomConfig, cfgErr := h.llmConfigService.GetFullConfigForUser(userID)
	if cfgErr == nil && hasCustomConfig && userConfig != nil {
		activeLLMClient = agentpkg.NewOpenAILLMClient(
			userConfig.APIKey, userConfig.BaseURL, userConfig.Model, 30*time.Second,
		)
		stepModel = userConfig.Model
	}

	// 执行 Agent(同步聚合 Run,内部 drain RunStream)
	result, err := h.agentService.Run(ctx, userID, toAgentMessages(ctxMessages), activeLLMClient)
	if err != nil {
		logger.ErrorWithFields("feishu: agent run failed, sending fallback",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		// 兜底回复,不落 assistant 消息(避免脏数据)
		_ = h.sender.SendText(ctx, in.OpenID, fallbackReply)
		return nil
	}

	// records → conversation.AgentStep 落库(segments=nil,IM 入口无交错段)
	steps := recordsToAgentSteps(result.Records, userID, stepModel)
	if err := h.messageService.SaveAssistantMessageWithSegments(
		ctx, userID, result.FinalResponse, nil, steps,
	); err != nil {
		logger.ErrorWithFields("feishu: save assistant message failed",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	// 回复用户。发送失败仅记日志,不上抛——消息已落库,后续可通过历史查看。
	if err := h.sender.SendText(ctx, in.OpenID, result.FinalResponse); err != nil {
		logger.WarnWithFields("feishu: send text failed",
			zap.Int64("user_id", userID),
			zap.String("open_id", in.OpenID),
			zap.Error(err),
		)
	}
	return nil
}

// toAgentMessages 把 llm.ChatMessage 转成 agent 包期望的 []map[string]interface{}。
// 与 web handler 同款转换(每个 segment 只携带 role+content,工具消息历史由 agent 自己重建)。
func toAgentMessages(messages []llm.ChatMessage) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		items = append(items, map[string]interface{}{"role": msg.Role, "content": msg.Content})
	}
	return items
}

// recordsToAgentSteps 把 agent 聚合产出的 StepRecord 链转成可落库的 conversation.AgentStep
// 链(同款语义,与 web handler 同名 helper 行为一致)。MessageID 由 service 层 stamp。
//
// v1.6 思考:helper 在 web 和 feishu 各一份是为保持包独立性(避免 feishu→web 反向依赖,
// 也避免把存储型 entity conversation.AgentStep 拖进 agent 包语义)。逻辑稳定且只有几行,
// 重复可接受;若未来出现第 3 个入口,再抽到 chat service 层不晚。
func recordsToAgentSteps(records []agentpkg.StepRecord, userID int64, model string) []*conversation.AgentStep {
	if len(records) == 0 {
		return nil
	}
	steps := make([]*conversation.AgentStep, 0, len(records))
	for i, r := range records {
		var step *conversation.AgentStep
		switch r.Kind {
		case agentpkg.StepKindLLMCall:
			step = conversation.NewLLMStep(userID, r.Request, r.Response, model, r.Status, r.DurationMs)
		case agentpkg.StepKindToolCall:
			step = conversation.NewToolStep(userID, r.Tool, r.Request, r.Response, r.Status, r.DurationMs)
		default:
			continue
		}
		step.Seq = i
		steps = append(steps, step)
	}
	return steps
}
