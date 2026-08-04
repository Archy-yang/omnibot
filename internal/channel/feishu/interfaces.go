// Package feishu implements the Feishu (Lark) bot channel for OmniBot (v1.6).
//
// 飞书机器人通过长连接(WebSocket)接收用户消息,复用 v1.5 Agent 同步路径处理消息,
// 把最终文本通过飞书发消息 API 回复。运行链路记录与 web 同步端点对齐——同步 Run 是
// RunStream 之上的聚合层,handler 把 result.Records 转 conversation.AgentStep 落库。
//
// 包内分层:
//   - MessageHandler.HandleInbound: 纯逻辑 pipeline,所有外部依赖走接口,完全可测
//   - Channel.Start: 启动 SDK 长连接 goroutine,把 SDK 事件解析后调 HandleInbound
//   - Sender 接口: 隔离 SDK 发消息 API,测试用 mock
package feishu

import (
	"context"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	agentpkg "omnibot/internal/service/agent"
	userservice "omnibot/internal/service/user"
)

// BindingService 账号绑定服务接口(v2.2 飞书引入,v2.3 泛化为渠道通用)。
// 飞书消息不再自动建号:先解析绑定码格式 -> 走 BindChannel;
// 否则 ResolveUserID 查已绑定身份,未绑定回引导不建号(PRD 5.4)。
type BindingService interface {
	// BindChannel 提交绑定码完成渠道号与 web 账号绑定(channelType 由调用方传)。
	// 返回 nil 成功;ErrCodeInvalid/ErrChannelAlreadyBound/ErrAccountAlreadyBound 对应 PRD 5.2。
	BindChannel(channelType, code, openID string) error
	// ResolveUserID 解析渠道身份:已绑返 (userID,true);未绑返 (0,false)。
	ResolveUserID(channelType, openID string) (userID int64, bound bool, err error)
}

// MessageService 消息服务接口。SaveUserMessage 用飞书 message_id 做去重。
// SaveAssistantMessageWithSegments 与 web 同步端点共用,segments=nil(IM 入口无交错段)。
// SaveReportMessage 供飞书任务完成推送时落汇报消息(Kind=report,关联 task_id)。
type MessageService interface {
	SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)
	SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
	SaveAssistantMessageWithToolCalls(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, toolCalls *string, steps []*conversation.AgentStep) error
	SaveReportMessage(ctx context.Context, userID, taskID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
}

// AgentService Agent 服务接口(仅同步 Run,飞书不走流式)。签名与 web AgentService 一致。
type AgentService interface {
	Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...agentpkg.LLMClient) (*agentpkg.AgentResult, error)
}

// LLMConfigService 用户 LLM 配置查询(用户自定义优先)。
type LLMConfigService interface {
	GetFullConfigForUser(userID int64) (*userservice.FullLLMConfig, bool, error)
}

// SubAgentReportProvider 后台 Agent 框架前置汇报接口(08 §4.5)。
// 飞书主对话 handler 在调主 Agent Run 前查询未汇报任务,有则注入回执让主 Agent 先汇报。
// nil 时不启用前置汇报(向后兼容,未装配后台 Agent 框架时)。
type SubAgentReportProvider interface {
	// GetPendingReportContext 返回待汇报的回执指令 + 对应任务 ID。无则 instruction 为空。
	GetPendingReportContext(userID int64) (instruction string, taskIDs []int64)
	// MarkReported 标记任务已汇报(防重复)。
	MarkReported(taskID int64) error
}

// Sender 飞书发消息接口。隔离 SDK,测试 mock。
//
// SendText  — 纯文本消息(MsgType="text")。飞书客户端**不渲染 markdown**,
//
//	适合 fallback 兜底("服务暂时不可用")等短文本。
//
// SendMarkdown — interactive 卡片(MsgType="interactive",含 markdown element),
//
//	飞书客户端**渲染 markdown**:加粗/列表/链接/代码块等。Agent 输出几乎
//	总是 markdown,默认走这条路径。
type Sender interface {
	SendText(ctx context.Context, openID, content string) error
	SendMarkdown(ctx context.Context, openID, content string) error
	SendCard(ctx context.Context, openID, title, content, template string) error
}
