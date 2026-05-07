package llm

import "context"

// ChatMessage 单轮对话消息
type ChatMessage struct {
	Role    string // system / user / assistant
	Content string // 消息文本
}

// LLMProvider 大语言模型提供者统一接口
type LLMProvider interface {
	// ChatCompletion 对话补全
	// ctx: 上下文，支持超时取消
	// messages: 对话历史列表，最后一条是用户当前提问
	// 返回: 模型生成的回复文本
	ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
}
