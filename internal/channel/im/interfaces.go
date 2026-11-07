// Package im 提供渠道无关的 IM 入站处理 pipeline。
//
// 31 号 N1:飞书 handler 的 HandleInbound 链路(身份解析 → 绑定码 → 反馈指令 →
// 记忆命令 → 落库 → 上下文 → 前置汇报 → 选 LLM → Agent.Run → 落库 → 回复)本就全部
// 走接口,提到本包后飞书/微信 ClawBot/后续任意 IM 渠道共用一套,避免三处同步改逻辑。
//
// 包内不含任何 SDK 依赖:发送能力由调用方注入 Sender 实现,身份解析由
// BindingService 完成,渠道差异只体现在 Options.ChannelType / Source / 文案上。
package im

import (
	"context"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	agentpkg "omnibot/internal/service/agent"
	userservice "omnibot/internal/service/user"
)

// InboundMessage 渠道无关的入站消息。
//
// 各渠道协议层把 SDK/HTTP 结构解析成这个中性结构后交给 Handler.HandleInbound,
// 使业务链路完全可测(测试不需要任何真实渠道)。
type InboundMessage struct {
	OpenID   string // 渠道内用户身份(飞书 open_id / ClawBot ilink user id)
	Text     string // 文本内容(非文本消息由渠道层先过滤或给占位文案)
	MsgID    string // 渠道消息 id,用作 SaveUserMessage 的去重键
	ChatType string // "p2p" / "group" / ...,本期只处理 p2p
}

// BindingService 账号绑定服务接口(v2.2 飞书引入,v2.3 泛化为渠道通用)。
// IM 消息不再自动建号:先解析绑定码格式 -> 走 BindChannel;
// 否则 ResolveUserID 查已绑定身份,未绑定回引导不建号(PRD 5.4)。
type BindingService interface {
	// BindChannel 提交绑定码完成渠道号与 web 账号绑定(channelType 由 handler 传)。
	BindChannel(channelType, code, openID string) error
	// ResolveUserID 解析渠道身份:已绑返 (userID,true);未绑返 (0,false)。
	ResolveUserID(channelType, openID string) (userID int64, bound bool, err error)
}

// MessageService 消息服务接口(IM 入口只用到落库 + 建上下文)。
// SaveUserMessage 用渠道 message_id 去重;segments=nil(IM 入口无交错段)。
type MessageService interface {
	// SaveUserMessage 保存用户消息并开启新逻辑 Turn,返回 TurnID(0=Turn 创建失败不阻塞)。
	SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) (int64, error)
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)
	SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
}

// AgentService Agent 服务接口(仅同步 Run,IM 渠道不走流式)。签名与 web AgentService 一致。
type AgentService interface {
	Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...agentpkg.LLMClient) (*agentpkg.AgentResult, error)
}

// LLMConfigService 用户 LLM 配置查询(用户自定义优先)。
type LLMConfigService interface {
	GetFullConfigForUser(userID int64) (*userservice.FullLLMConfig, bool, error)
}

// SubAgentReportProvider 后台 Agent 框架前置汇报接口(08 §4.5)。
// nil 时不启用前置汇报(向后兼容,未装配后台 Agent 框架时)。
type SubAgentReportProvider interface {
	// GetPendingReportContext 返回待汇报的回执指令 + 对应任务 ID。无则 instruction 为空。
	GetPendingReportContext(userID int64, source string) (instruction string, taskIDs []int64)
	// MarkReported 标记任务已汇报(防重复)。
	MarkReported(taskID int64) error
}

// MemoryCommandService 记忆管理命令接口(高级记忆系统PRD AC4.3)。
// 用户在对话里管理记忆:#记住 / #我的记忆 / #清空记忆 / #删除记忆 N。nil 时不启用。
type MemoryCommandService interface {
	Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error)
	List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error)
	Clear(ctx context.Context, userID int64) error
	Delete(ctx context.Context, userID int64, memoryID int64) (bool, error)
}

// LarkAuthCommander IM 内重新发起飞书授权(device flow)的窄面。
// 授权必须用户本人在浏览器确认,IM 侧只负责把验证链接送出去。
type LarkAuthCommander interface {
	Relogin(ctx context.Context, userID int64) (verifyURL, userCode string, err error)
}

// NoteFeedbackService 主动消息反馈窄面(service/butler SettingsService 实现)。
type NoteFeedbackService interface {
	// FeedbackLatestDeliveredNote 反馈落到该用户最近一条 delivered 主动消息,返回确认文案。
	FeedbackLatestDeliveredNote(userID int64, feedback string) (string, error)
}

// Sender 出站发送接口。隔离各渠道 SDK/HTTP,测试用 mock。
//
// SendText     — 纯文本消息。适合 fallback 兜底("服务暂时不可用")等短文本。
// SendMarkdown — 支持 markdown 渲染的形态(飞书卡片;ClawBot 等价纯文本)。
//
//	Agent 输出几乎总是 markdown,默认走这条路径。
//
// SendCard     — 带标题的卡片形态(ClawBot 降级为文本)。
type Sender interface {
	SendText(ctx context.Context, openID, content string) error
	SendMarkdown(ctx context.Context, openID, content string) error
	SendCard(ctx context.Context, openID, title, content, template string) error
}
