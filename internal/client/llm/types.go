package llm

import (
	"context"
	"errors"
)

// ErrStreamingNotSupported 表示该 provider 不支持流式输出
var ErrStreamingNotSupported = errors.New("streaming not supported by this provider")

// ChatMessage 单轮对话消息(OpenAI chat 协议格式)
type ChatMessage struct {
	Role       string                   // system / user / assistant / tool
	Content    string                   // 消息文本
	ToolCalls  []map[string]interface{} // assistant 消息:工具调用 [{id,type,function:{name,arguments}}],规范改造
	ToolCallID string                   // tool 消息:对应的 tool_call_id,规范改造
}

// StreamChunk 流式响应的单个片段
type StreamChunk struct {
	Content string // 本次推送的增量文本
	Done    bool   // 是否为最后一片
	Error   error  // 流式过程中发生的错误
}

// LLMProvider 大语言模型提供者统一接口
type LLMProvider interface {
	// ChatCompletion 对话补全（同步）
	// ctx: 上下文，支持超时取消
	// messages: 对话历史列表，最后一条是用户当前提问
	// 返回: 模型生成的回复文本
	ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)

	// StreamChatCompletion 流式对话补全
	// 返回一个只读 channel，逐片推送模型生成的文本
	// 调用方需要持续读取 channel 直到关闭
	// 如果 provider 不支持流式，返回 ErrStreamingNotSupported
	StreamChatCompletion(ctx context.Context, messages []ChatMessage) (<-chan StreamChunk, error)
}
