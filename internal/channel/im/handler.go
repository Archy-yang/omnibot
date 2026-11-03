package im

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
	butlerdomain "omnibot/internal/domain/butler"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	usersvc "omnibot/internal/service/user"
	"omnibot/pkg/logger"
)

// fallbackReplyAgent agent 执行失败时给用户的兜底文案——与 web 端「服务暂时不可用」对齐,
// 避免无反馈;不透传内部错误细节(安全红线)。
const fallbackReplyAgent = "服务暂时不可用,请稍后再试"

// 绑定相关通用文案(PRD 5.2 / 5.4)。渠道差异部分由 Options 覆盖。
const (
	defaultBindSuccessReply    = "绑定成功!现在可以跟我聊天了"
	defaultUnboundGuide        = `你还没有绑定 OmniBot 账号。请先在 web 端登录,在设置里获取绑定码,然后在这里发送「绑定 + 空格 + 绑定码」(例如 绑定 123456)完成绑定。`
	defaultBindCodeInvalidText = "绑定码无效或已过期,请在 web 端重新获取"
	defaultAccountBoundText    = "你的账号已绑定其他渠道,如需更换请稍后(暂不支持)"
)

// 绑定码格式:「绑定」+ 空格 + 6 位数字(PRD 4.3)。TrimSpace 后匹配。
var bindCodeRe = regexp.MustCompile(`^绑定 (\d{6})$`)

// feedbackIntentMap 送达反馈文本意图(精确匹配,29号 N4)。
var feedbackIntentMap = map[string]string{
	"有用":   butlerdomain.NoteFeedbackPositive,
	"赞":    butlerdomain.NoteFeedbackPositive,
	"不用再提": butlerdomain.NoteFeedbackNegative,
	"别再提":  butlerdomain.NoteFeedbackNegative,
	"没用":   butlerdomain.NoteFeedbackNegative,
}

// Options 构造 Handler 的渠道差异配置。业务链路完全一致,只有这些参数不同。
type Options struct {
	// ChannelType 通道类型,写入 user_channels.channel_type("feishu" / "wechat_claw")。
	ChannelType string
	// Source 后台任务的来源渠道标记(domainagent.Source*),子 Agent 完成后据此推送。
	Source string

	Binding   BindingService
	Messages  MessageService
	Agent     AgentService
	LLMConfig LLMConfigService
	Sender    Sender

	// BindSuccessReply 绑定成功文案(渠道专属,如「现在可以在飞书跟我聊了」)。
	BindSuccessReply string
	// AlreadyBoundReply 该渠道号已绑定其他账号的文案(渠道专属)。
	AlreadyBoundReply string
	// AccountAlreadyBoundReply 账号已绑该渠道的文案。
	AccountAlreadyBoundReply string
	// UnboundGuide 未绑定引导文案。
	UnboundGuide string
}

func (o Options) withDefaults() Options {
	if o.BindSuccessReply == "" {
		o.BindSuccessReply = defaultBindSuccessReply
	}
	if o.AlreadyBoundReply == "" {
		o.AlreadyBoundReply = "该渠道号已绑定其他账号"
	}
	if o.AccountAlreadyBoundReply == "" {
		o.AccountAlreadyBoundReply = defaultAccountBoundText
	}
	if o.UnboundGuide == "" {
		o.UnboundGuide = defaultUnboundGuide
	}
	return o
}

// Handler 渠道无关的入站 pipeline。所有依赖走接口,完全可单测。
type Handler struct {
	opts Options
	// 后台 Agent 框架前置汇报(08 §4.5)。nil 时不启用(向后兼容)。
	subAgentReporter SubAgentReportProvider
	// 记忆管理命令(高级记忆系统PRD AC4.3)。nil 时不启用。
	memoryService MemoryCommandService
	// 主动消息反馈文本通道(29号 N4)。nil 时不启用(不拦,走正常对话)。
	noteFeedbackSvc NoteFeedbackService
	// 飞书授权重发(19 号补强):#飞书授权 → 返回 device flow 验证链接。nil 时不启用。
	larkAuthSvc LarkAuthCommander
}

// NewHandler 创建入站处理器。
func NewHandler(opts Options) *Handler {
	return &Handler{opts: opts.withDefaults()}
}

// SetSubAgentReporter 注入后台 Agent 框架前置汇报依赖(08 §4.5)。
// 未调用时 subAgentReporter 为 nil,行为与之前一致(无前置汇报)。
func (h *Handler) SetSubAgentReporter(reporter SubAgentReportProvider) {
	h.subAgentReporter = reporter
}

// SetMemoryService 注入记忆管理命令依赖(PRD AC4.3)。未调用时无记忆命令。
func (h *Handler) SetMemoryService(svc MemoryCommandService) {
	h.memoryService = svc
}

// SetLarkAuthCommander 注入飞书授权重发能力(19 号补强)。未调用时 #飞书授权 不拦截。
func (h *Handler) SetLarkAuthCommander(svc LarkAuthCommander) {
	h.larkAuthSvc = svc
}

// SetNoteFeedbackService 注入主动消息反馈依赖(29号 N4)。未调用时反馈指令不拦。
func (h *Handler) SetNoteFeedbackService(svc NoteFeedbackService) {
	h.noteFeedbackSvc = svc
}

// HandleInbound IM 消息 pipeline。流程严格镜像 web/HandleSendMessageAgent:
//
//	绑定码/已绑身份解析 → SaveUserMessage(去重) → BuildContextMessages →
//	选 LLM(自定义优先) → AgentService.Run → records→steps 落 agent_steps + assistant 消息
//	→ Sender 回复最终文本
//
// 返回的 error 是「值得让上层重试或关连接」的硬错误;预期事件(重复消息/agent 失败/
// 发送失败)在内部消化并返回 nil。空文本、群聊也返回 nil(过滤)。
func (h *Handler) HandleInbound(ctx context.Context, in InboundMessage) error {
	// 本期仅处理单聊;群聊留后续(@ 解析、群成员校验等复杂度独立做)
	if in.ChatType != "p2p" {
		return nil
	}
	// 空文本不触发 agent
	if strings.TrimSpace(in.Text) == "" {
		return nil
	}

	// 身份解析。先看是否绑定码格式,再看是否已绑定,最后未绑定回引导(不建号)。
	text := strings.TrimSpace(in.Text)
	if code, ok := parseBindCode(text); ok {
		return h.handleBindCode(ctx, in.OpenID, code)
	}
	userID, bound, err := h.opts.Binding.ResolveUserID(h.opts.ChannelType, in.OpenID)
	if err != nil {
		logger.ErrorWithFields("im: resolve user failed",
			zap.String("channel", h.opts.ChannelType),
			zap.String("open_id", in.OpenID),
			zap.Error(err),
		)
		return err
	}
	if !bound {
		// 未绑定:不建号、不进对话,回绑定引导(PRD 5.4)
		_ = h.opts.Sender.SendText(ctx, in.OpenID, h.opts.UnboundGuide)
		return nil
	}

	// 主动消息反馈(29号 N4):「有用/不用再提」落到最近一条 delivered 主动消息,先于对话处理;
	// 精确匹配防误伤,svc 未注入(管家未装配)不拦
	if reply, handled := h.HandleNoteFeedback(userID, text); handled {
		if err := h.opts.Sender.SendText(ctx, in.OpenID, reply); err != nil {
			logger.ErrorWithFields("im: send note feedback reply failed",
				zap.String("channel", h.opts.ChannelType),
				zap.String("open_id", in.OpenID), zap.Error(err))
		}
		return nil
	}

	// 记忆管理命令(高级记忆系统PRD AC4.3):先于对话处理,nil 时不启用
	if h.memoryService != nil {
		if reply, handled := h.HandleMemoryCommand(ctx, userID, text); handled {
			if err := h.opts.Sender.SendText(ctx, in.OpenID, reply); err != nil {
				logger.ErrorWithFields("im: send memory command reply failed",
					zap.String("channel", h.opts.ChannelType),
					zap.String("open_id", in.OpenID), zap.Error(err))
			}
			return nil
		}
	}

	// 飞书授权重发(19 号补强):IM 里发「#飞书授权」拿验证链接——
	// 之前授权失效只能去 web 重连,且 IM 的 auth 子命令被工具白名单挡死,用户无路可走。
	if h.larkAuthSvc != nil && text == "#飞书授权" {
		url, code, err := h.larkAuthSvc.Relogin(ctx, userID)
		reply := ""
		switch {
		case err != nil:
			logger.ErrorWithFields("im: lark relogin failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID), zap.Error(err))
			reply = "发起飞书授权失败，请稍后在 Web 设置里重试。"
		case url == "":
			reply = "已发起授权，但没拿到验证链接，请稍后在 Web 设置里重试。"
		case code != "":
			reply = fmt.Sprintf("飞书授权需要你本人确认：\n%s\n\n用户码：%s\n确认后回来告诉我一声即可。", url, code)
		default:
			reply = fmt.Sprintf("飞书授权需要你本人确认：\n%s\n\n确认后回来告诉我一声即可。", url)
		}
		if sendErr := h.opts.Sender.SendText(ctx, in.OpenID, reply); sendErr != nil {
			logger.WarnWithFields("im: send lark auth reply failed",
				zap.String("channel", h.opts.ChannelType),
				zap.String("open_id", in.OpenID), zap.Error(sendErr))
		}
		return nil
	}

	// 保存用户消息(渠道 message_id 做幂等),返回逻辑 TurnID(Phase 1,§5.4)
	turnID, err := h.opts.Messages.SaveUserMessage(ctx, userID, in.Text, in.MsgID)
	if err != nil {
		if errors.Is(err, chatsvc.ErrDuplicateMessage) {
			// SDK/长轮询可能重投同一条消息;静默丢弃,不重复回复
			logger.InfoWithFields("im: duplicate message ignored",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID),
				zap.String("msg_id", in.MsgID),
			)
			return nil
		}
		// 其他错误仅记录,继续走 agent(宁可重复也不丢消息)
		logger.WarnWithFields("im: save user message failed, continuing",
			zap.String("channel", h.opts.ChannelType),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	ctxMessages, err := h.opts.Messages.BuildContextMessages(ctx, userID, in.Text)
	if err != nil {
		ctxMessages = []llm.ChatMessage{{Role: "user", Content: in.Text}}
	}

	// 后台 Agent 框架前置汇报兜底(08 §4.5):查未汇报任务,有则把回执注入上下文,
	// 主 Agent 同步 Run 时先汇报再回应当前消息。汇报后标记 reported(防重复)。
	var reportedTaskIDs []int64
	if h.subAgentReporter != nil {
		instruction, taskIDs := h.subAgentReporter.GetPendingReportContext(userID, h.opts.Source)
		if instruction != "" {
			ctxMessages = append([]llm.ChatMessage{{Role: "system", Content: instruction}}, ctxMessages...)
			reportedTaskIDs = taskIDs
		}
	}

	// 选 LLM:用户自定义配置优先(与 web 同步端点完全一致)
	var activeLLMClient agentpkg.LLMClient
	stepModel := ""
	userConfig, hasCustomConfig, cfgErr := h.opts.LLMConfig.GetFullConfigForUser(userID)
	if cfgErr == nil && hasCustomConfig && userConfig != nil {
		activeLLMClient = agentpkg.NewOpenAILLMClient(
			userConfig.APIKey, userConfig.BaseURL, userConfig.Model, 30*time.Second,
		)
		stepModel = userConfig.Model
	}

	// 执行 Agent(同步聚合 Run,内部 drain RunStream)
	// 注入来源渠道 + 推送目标:delegate 派活时记录到 task,子 Agent 完成后主动推送回该渠道。
	ctx = agentpkg.WithSource(ctx, h.opts.Source)
	ctx = agentpkg.WithNotifyTarget(ctx, in.OpenID)
	if turnID > 0 {
		ctx = agentpkg.WithTurnID(ctx, turnID)
	}
	result, err := h.opts.Agent.Run(ctx, userID, ToAgentMessages(ctxMessages), activeLLMClient)
	if err != nil {
		logger.ErrorWithFields("im: agent run failed, sending fallback",
			zap.String("channel", h.opts.ChannelType),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		// 兜底回复,不落 assistant 消息(避免脏数据)
		_ = h.opts.Sender.SendText(ctx, in.OpenID, fallbackReplyAgent)
		return nil
	}

	// records → conversation.AgentStep 落库(segments=nil,IM 入口无交错段)
	steps := RecordsToAgentSteps(result.Records, userID, stepModel)
	if err := h.opts.Messages.SaveAssistantMessageWithSegments(
		ctx, userID, result.FinalResponse, nil, steps,
	); err != nil {
		logger.ErrorWithFields("im: save assistant message failed",
			zap.String("channel", h.opts.ChannelType),
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	// 后台 Agent 框架:前置汇报标记 reported(08 §4.5)。主 Agent 已在本次回复里汇报,
	// 标记这些任务已汇报,防下次重复。
	for _, tid := range reportedTaskIDs {
		if err := h.subAgentReporter.MarkReported(tid); err != nil {
			logger.ErrorWithFields("im: mark task reported failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("task_id", tid), zap.Error(err))
		}
	}

	// 回复用户。发送失败仅记日志,不上抛——消息已落库,后续可通过历史查看。
	if err := h.opts.Sender.SendMarkdown(ctx, in.OpenID, result.FinalResponse); err != nil {
		logger.WarnWithFields("im: send reply failed",
			zap.String("channel", h.opts.ChannelType),
			zap.Int64("user_id", userID),
			zap.String("open_id", in.OpenID),
			zap.Error(err),
		)
	}
	return nil
}

// handleNoteFeedback 反馈指令钩子:命中且 svc 已装配 → 返回确认文案并拦下(不进对话);
// 未命中/未装配 → 不拦。ErrNoteNotFound 等失败给兜底文案(仍拦下——意图已明确表达)。
func (h *Handler) HandleNoteFeedback(userID int64, text string) (string, bool) {
	if h.noteFeedbackSvc == nil {
		return "", false
	}
	fb, ok := feedbackIntentMap[text]
	if !ok {
		return "", false
	}
	reply, err := h.noteFeedbackSvc.FeedbackLatestDeliveredNote(userID, fb)
	if err != nil {
		return "最近没有可反馈的主动消息。", true
	}
	return reply, true
}

// handleMemoryCommand 记忆管理命令(与微信命令体系对齐,PRD AC4.3)。
// 返回 handled=true 表示已按命令处理,调用方直接回复不再进对话。
func (h *Handler) HandleMemoryCommand(ctx context.Context, userID int64, text string) (string, bool) {
	trimmed := strings.TrimSpace(text)
	switch {
	case strings.HasPrefix(trimmed, "#记住"):
		content := strings.TrimSpace(strings.TrimPrefix(trimmed, "#记住"))
		if content == "" {
			return "请这样告诉我：#记住 你想让我长期记住的内容", true
		}
		memory, err := h.memoryService.Remember(ctx, userID, content)
		if err != nil {
			logger.ErrorWithFields("im: memory remember failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReplyAgent, true
		}
		return fmt.Sprintf("已记住：%s\n\n提醒：请不要保存密码、API Key、身份证号等敏感信息。", memory.Content), true
	case trimmed == "#我的记忆":
		memories, err := h.memoryService.List(ctx, userID)
		if err != nil {
			logger.ErrorWithFields("im: memory list failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReplyAgent, true
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
			logger.ErrorWithFields("im: memory clear failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReplyAgent, true
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
			return fallbackReplyAgent, true
		}
		if index > len(memories) {
			return "记忆序号不存在，请发送 #我的记忆 查看当前列表。", true
		}
		selected := memories[index-1]
		if _, err := h.memoryService.Delete(ctx, userID, selected.ID); err != nil {
			logger.ErrorWithFields("im: memory delete failed",
				zap.String("channel", h.opts.ChannelType),
				zap.Int64("user_id", userID), zap.Error(err))
			return fallbackReplyAgent, true
		}
		return fmt.Sprintf("已删除第 %d 条记忆：%s", index, selected.Content), true
	}
	return "", false
}

// handleBindCode 处理绑定码提交,按 PRD 5.2 映射回复文案。
func (h *Handler) handleBindCode(ctx context.Context, openID, code string) error {
	err := h.opts.Binding.BindChannel(h.opts.ChannelType, code, openID)
	reply := h.opts.BindSuccessReply
	switch {
	case err == nil:
		// 成功
	case errors.Is(err, usersvc.ErrCodeInvalid):
		reply = defaultBindCodeInvalidText
	case errors.Is(err, usersvc.ErrChannelAlreadyBound):
		reply = h.opts.AlreadyBoundReply
	case errors.Is(err, usersvc.ErrAccountAlreadyBound):
		reply = h.opts.AccountAlreadyBoundReply
	default:
		logger.ErrorWithFields("im: bind failed",
			zap.String("channel", h.opts.ChannelType),
			zap.String("open_id", openID),
			zap.Error(err),
		)
		reply = fallbackReplyAgent
	}
	if sendErr := h.opts.Sender.SendText(ctx, openID, reply); sendErr != nil {
		logger.WarnWithFields("im: send bind reply failed",
			zap.String("channel", h.opts.ChannelType),
			zap.String("open_id", openID),
			zap.Error(sendErr),
		)
	}
	return nil
}

func parseBindCode(text string) (string, bool) {
	m := bindCodeRe.FindStringSubmatch(text)
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// toAgentMessages 把 llm.ChatMessage 转成 agent 包期望的 []map[string]interface{}。
// 与 web handler 同款转换(每个 segment 只携带 role+content,工具消息历史由 agent 自己重建)。
func ToAgentMessages(messages []llm.ChatMessage) []map[string]interface{} {
	return agentpkg.ChatMessagesToAgent(messages)
}

// recordsToAgentSteps 主 Agent 步骤转换(委托 agent.StepRecordsToAgentSteps 公共函数)。
// MessageID 由 service 层 stamp。
func RecordsToAgentSteps(records []agentpkg.StepRecord, userID int64, model string) []*conversation.AgentStep {
	return agentpkg.StepRecordsToAgentSteps(records, userID, model)
}
