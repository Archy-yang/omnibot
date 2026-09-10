package feishu

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"omnibot/internal/client/llm"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	usersvc "omnibot/internal/service/user"
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
	bindingService   BindingService
	messageService   MessageService
	agentService     AgentService
	llmConfigService LLMConfigService
	sender           Sender
	// 后台 Agent 框架前置汇报(08 §4.5)。nil 时不启用(向后兼容)。
	subAgentReporter SubAgentReportProvider
	// 记忆管理命令(高级记忆系统PRD AC4.3)。nil 时不启用。
	memoryService MemoryCommandService
}

// NewMessageHandler 创建飞书消息处理器。
func NewMessageHandler(
	binding BindingService,
	msg MessageService,
	agent AgentService,
	cfg LLMConfigService,
	sender Sender,
) *MessageHandler {
	return &MessageHandler{
		bindingService:   binding,
		messageService:   msg,
		agentService:     agent,
		llmConfigService: cfg,
		sender:           sender,
	}
}

// SetSubAgentReporter 注入后台 Agent 框架前置汇报依赖(08 §4.5)。
// 未调用时 subAgentReporter 为 nil,行为与之前一致(无前置汇报)。
func (h *MessageHandler) SetSubAgentReporter(reporter SubAgentReportProvider) {
	h.subAgentReporter = reporter
}

// SetMemoryService 注入记忆管理命令依赖(PRD AC4.3)。未调用时无记忆命令。
func (h *MessageHandler) SetMemoryService(svc MemoryCommandService) {
	h.memoryService = svc
}

// fallbackReply 是 agent 执行失败时给用户的兜底文案——和 web 端「服务暂时不可用」对齐,
// 避免无反馈;不透传内部错误细节(安全红线)。
const fallbackReply = "服务暂时不可用,请稍后再试"

// handleMemoryCommand 飞书记忆管理命令(与微信命令体系对齐,PRD AC4.3)。
// 返回 handled=true 表示已按命令处理,调用方直接回复不再进对话。
func (h *MessageHandler) handleMemoryCommand(ctx context.Context, userID int64, text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "#记住"):
		content := strings.TrimSpace(strings.TrimPrefix(trimmed, "#记住"))
		if content == "" {
			return "请这样告诉我：#记住 你想让我长期记住的内容", true
		}
		memory, err := h.memoryService.Remember(ctx, userID, content)
		if err != nil {
			logger.ErrorWithFields("feishu: memory remember failed",
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReply, true
		}
		return fmt.Sprintf("已记住：%s\n\n提醒：请不要保存密码、API Key、身份证号等敏感信息。", memory.Content), true
	case trimmed == "#我的记忆":
		memories, err := h.memoryService.List(ctx, userID)
		if err != nil {
			logger.ErrorWithFields("feishu: memory list failed",
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReply, true
		}
		if len(memories) == 0 {
			return "我还没有长期记住任何信息。\n\n你可以这样告诉我：\n#记住 我偏好简洁直接的回答", true
		}
		var b strings.Builder
		b.WriteString("我目前记住了这些信息：\n\n")
		for i, m := range memories {
			tag := ""
			if m.Source == memorydomain.MemorySourceAuto {
				tag = "（自动记忆）"
			}
			fmt.Fprintf(&b, "%d. %s%s（记录于 %s）", i+1, m.Content, tag, m.CreatedAt.Format("2006-01-02"))
			if i < len(memories)-1 {
				b.WriteString("\n")
			}
		}
		return b.String(), true
	case trimmed == "#清空记忆":
		if err := h.memoryService.Clear(ctx, userID); err != nil {
			logger.ErrorWithFields("feishu: memory clear failed",
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReply, true
		}
		return "已清空你的全部长期记忆。", true
	case strings.HasPrefix(trimmed, "#删除记忆"):
		indexText := strings.TrimSpace(strings.TrimPrefix(trimmed, "#删除记忆"))
		index, err := strconv.Atoi(indexText)
		if err != nil || index <= 0 {
			return "", false // 格式不完整不拦截,留给人机对话
		}
		memories, err := h.memoryService.List(ctx, userID)
		if err != nil {
			return fallbackReply, true
		}
		if index > len(memories) {
			return "记忆序号不存在，请发送 #我的记忆 查看当前列表。", true
		}
		selected := memories[index-1]
		if _, err := h.memoryService.Delete(ctx, userID, selected.ID); err != nil {
			logger.ErrorWithFields("feishu: memory delete failed",
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReply, true
		}
		return fmt.Sprintf("已删除第 %d 条记忆：%s", index, selected.Content), true
	}
	return "", false
}

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

	// v2.2: 身份解析。先看是否绑定码格式,再看是否已绑定,最后未绑定回引导(不建号)。
	text := strings.TrimSpace(in.Text)
	if code, ok := parseBindCode(text); ok {
		return h.handleBindCode(ctx, in.OpenID, code)
	}
	userID, bound, err := h.bindingService.ResolveUserID("feishu", in.OpenID)
	if err != nil {
		logger.ErrorWithFields("feishu: resolve user failed",
			zap.String("open_id", in.OpenID),
			zap.Error(err),
		)
		return err
	}
	if !bound {
		// 未绑定:不建号、不进对话,回绑定引导(PRD 5.4)
		_ = h.sender.SendText(ctx, in.OpenID, unboundGuide)
		return nil
	}

	// 记忆管理命令(高级记忆系统PRD AC4.3):先于对话处理,nil 时不启用
	if h.memoryService != nil {
		if reply, handled := h.handleMemoryCommand(ctx, userID, text); handled {
			if err := h.sender.SendText(ctx, in.OpenID, reply); err != nil {
				logger.ErrorWithFields("feishu: send memory command reply failed",
					zap.String("open_id", in.OpenID), zap.Error(err))
			}
			return nil
		}
	}

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

	// 后台 Agent 框架前置汇报兜底(08 §4.5):查未汇报任务,有则把回执注入上下文,
	// 主 Agent 同步 Run 时先汇报再回应当前消息。汇报后标记 reported(防重复)。
	var reportedTaskIDs []int64
	if h.subAgentReporter != nil {
		instruction, taskIDs := h.subAgentReporter.GetPendingReportContext(userID)
		if instruction != "" {
			ctxMessages = append([]llm.ChatMessage{{Role: "system", Content: instruction}}, ctxMessages...)
			reportedTaskIDs = taskIDs
		}
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
	// 注入来源渠道(feishu)+ 推送目标(open_id)到 ctx:delegate 派活时记录到 task,
	// 子 Agent 完成后主动推送回飞书(方案A)。
	ctx = agentpkg.WithSource(ctx, domainagent.SourceFeishu)
	ctx = agentpkg.WithNotifyTarget(ctx, in.OpenID)
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

	// 后台 Agent 框架:前置汇报标记 reported(08 §4.5)。主 Agent 已在本次回复里汇报,
	// 标记这些任务已汇报,防下次重复。
	for _, tid := range reportedTaskIDs {
		if err := h.subAgentReporter.MarkReported(tid); err != nil {
			logger.ErrorWithFields("feishu: mark task reported failed",
				zap.Int64("task_id", tid), zap.Error(err))
		}
	}

	// 回复用户。发送失败仅记日志,不上抛——消息已落库,后续可通过历史查看。
	// 用 SendMarkdown:Agent 输出几乎总是 markdown(加粗/列表/链接/代码块),
	// 飞书纯文本消息不渲染,会让 `**bold**` 类符号在客户端原样显示——很丑;
	// interactive 卡片含 markdown element 才会渲染。fallback 兜底走 SendText
	// (短文本无需卡片包裹)。
	if err := h.sender.SendMarkdown(ctx, in.OpenID, result.FinalResponse); err != nil {
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
		m := map[string]interface{}{"role": msg.Role, "content": msg.Content}
		if len(msg.ToolCalls) > 0 {
			m["tool_calls"] = msg.ToolCalls
		}
		if msg.ToolCallID != "" {
			m["tool_call_id"] = msg.ToolCallID
		}
		items = append(items, m)
	}
	return items
}

// recordsToAgentSteps 主 Agent 步骤转换(委托 agent.StepRecordsToAgentSteps 公共函数)。
// MessageID 由 service 层 stamp。
func recordsToAgentSteps(records []agentpkg.StepRecord, userID int64, model string) []*conversation.AgentStep {
	return agentpkg.StepRecordsToAgentSteps(records, userID, model)
}

// 绑定码格式:「绑定」+ 空格 + 6 位数字(PRD 4.3)。TrimSpace 后匹配。
var bindCodeRe = regexp.MustCompile(`^绑定 (\d{6})$`)

func parseBindCode(text string) (string, bool) {
	m := bindCodeRe.FindStringSubmatch(text)
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// handleBindCode 处理绑定码提交,按 PRD 5.2 映射回复文案。
func (h *MessageHandler) handleBindCode(ctx context.Context, openID, code string) error {
	err := h.bindingService.BindChannel("feishu", code, openID)
	reply := bindSuccessReply
	switch {
	case err == nil:
		// 成功
	case errors.Is(err, usersvc.ErrCodeInvalid):
		reply = bindCodeInvalidReply
	case errors.Is(err, usersvc.ErrChannelAlreadyBound):
		reply = feishuAlreadyBoundReply
	case errors.Is(err, usersvc.ErrAccountAlreadyBound):
		reply = accountAlreadyBoundReply
	default:
		logger.ErrorWithFields("feishu: bind failed",
			zap.String("open_id", openID),
			zap.Error(err),
		)
		reply = fallbackReply
	}
	if sendErr := h.sender.SendText(ctx, openID, reply); sendErr != nil {
		logger.WarnWithFields("feishu: send bind reply failed",
			zap.String("open_id", openID),
			zap.Error(sendErr),
		)
	}
	return nil
}

// 绑定相关回复文案(PRD 5.2 / 5.4)
const (
	bindSuccessReply         = "绑定成功!现在可以在飞书跟我聊了"
	bindCodeInvalidReply     = "绑定码无效或已过期,请在 web 端重新获取"
	feishuAlreadyBoundReply  = "该飞书号已绑定其他账号"
	accountAlreadyBoundReply = "你的账号已绑定飞书,如需更换请稍后(暂不支持)"
	unboundGuide             = `你还没有绑定 OmniBot 账号。请先在 web 端登录,在设置里获取绑定码,然后在这里发送「绑定 + 空格 + 绑定码」(例如 绑定 123456)完成绑定。`
)
