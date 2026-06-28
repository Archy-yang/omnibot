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

	"omnibot/internal/domain/conversation"
	"omnibot/internal/domain/user"
	"omnibot/internal/client/llm"
	agentpkg "omnibot/internal/service/agent"
	userservice "omnibot/internal/service/user"
)

// UserService 用户服务接口(取 web handler 同款契约的子集)。
// 飞书消息走 GetOrCreateByChannel("feishu", openID) → user_id 复用全部跨入口能力。
type UserService interface {
	GetOrCreateByChannel(channelType, channelUserID string) (*user.User, *user.UserChannel, bool, error)
}

// MessageService 消息服务接口。SaveUserMessage 用飞书 message_id 做去重。
// SaveAssistantMessageWithSegments 与 web 同步端点共用,segments=nil(IM 入口无交错段)。
type MessageService interface {
	SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)
	SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
}

// AgentService Agent 服务接口(仅同步 Run,飞书不走流式)。签名与 web AgentService 一致。
type AgentService interface {
	Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...agentpkg.LLMClient) (*agentpkg.AgentResult, error)
}

// LLMConfigService 用户 LLM 配置查询(用户自定义优先)。
type LLMConfigService interface {
	GetFullConfigForUser(userID int64) (*userservice.FullLLMConfig, bool, error)
}

// Sender 飞书发消息接口。隔离 SDK,测试 mock。
//
// SendText  — 纯文本消息(MsgType="text")。飞书客户端**不渲染 markdown**,
//             适合 fallback 兜底("服务暂时不可用")等短文本。
// SendMarkdown — interactive 卡片(MsgType="interactive",含 markdown element),
//             飞书客户端**渲染 markdown**:加粗/列表/链接/代码块等。Agent 输出几乎
//             总是 markdown,默认走这条路径。
type Sender interface {
	SendText(ctx context.Context, openID, content string) error
	SendMarkdown(ctx context.Context, openID, content string) error
}
