package agent

import "context"

// LLMStreamChunk 是 LLM 流式响应的原始增量单元，对应 OpenAI SSE 协议中
// 一行 `data: {...}` 解析后的内容。一个 chunk 要么是文本增量，要么是工具调用增量，
// 要么是终止信号。
//
// 解析规则（OpenAI 兼容协议）：
//   - delta.content != "" → ContentDelta
//   - delta.tool_calls 存在 → ToolCallDelta（同一轮可能多个 tool call，用 Index 区分）
//   - finish_reason 非空 → FinishReason 设值；data: [DONE] 单独触发 Done=true
type LLMStreamChunk struct {
	ContentDelta  string         // 文本 token 增量
	ToolCallDelta *ToolCallDelta // 工具调用增量（指针：nil 表示本 chunk 不是工具增量）
	FinishReason  string         // "stop" / "tool_calls" / "length" 等
	Done          bool           // true 表示 [DONE] 信号，channel 即将关闭
	Error         error          // 解析或网络错误，发生即终止流
}

// ToolCallDelta 是单次 tool_call 的增量。一次完整的 tool_call 通常跨多个 chunk：
//   - 首 chunk：携带 Index + ID + Name
//   - 后续 chunk：仅 Index + ArgumentsDelta（JSON 字符串拼接增量）
//
// 上层需按 Index 累积 ArgumentsDelta 直到收到 finish_reason="tool_calls"。
type ToolCallDelta struct {
	Index          int    // 第几个 tool call（OpenAI 允许同一轮多个）
	ID             string // 完整 ID，首个 chunk 给
	Name           string // 函数名，首个 chunk 给
	ArgumentsDelta string // arguments JSON 字符串增量
}

// StreamingLLMClient 流式 LLM 客户端接口。
// 实现方负责发起 stream=true 的 LLM 请求，按行解析 SSE 并把 LLMStreamChunk 推到 channel。
// channel 必须由实现方关闭（无论是正常结束还是出错，错误通过最后一个 chunk 的 Error 字段传递）。
//
// 现有同步 LLMClient 接口保持不变，本接口是并存关系，不强制实现。
type StreamingLLMClient interface {
	ChatCompletionStream(
		ctx context.Context,
		messages []map[string]interface{},
		tools []map[string]interface{},
	) (<-chan LLMStreamChunk, error)
}

// AgentEventType 是 ReActAgent 流式输出事件的类型枚举。
type AgentEventType string

const (
	// AgentEventToken：LLM 在最终回答阶段吐出的文本 token。前端按字符级拼接渲染。
	AgentEventToken AgentEventType = "token"

	// AgentEventToolCall：LLM 决定调用某个工具，准备执行（用户友好的「正在调用 xxx」状态条）。
	AgentEventToolCall AgentEventType = "tool_call"

	// AgentEventToolResult：工具执行完成，附带原始结果。前端默认不展示，留作调试或后续展开 UI。
	AgentEventToolResult AgentEventType = "tool_result"

	// AgentEventDone：整个 ReAct 循环结束，Content 字段携带最终拼接的完整文本，供 handler
	// 用于持久化（SaveAssistantMessage）。
	AgentEventDone AgentEventType = "done"

	// AgentEventError：流式过程中发生错误，channel 即将关闭。
	AgentEventError AgentEventType = "error"
)

// AgentEvent 是 ReActAgent.RunStream 推给上游的高层事件。
// 字段语义按 Type 区分：
//   - Token       → Content 是 token 增量
//   - ToolCall    → ToolName + ToolLabel
//   - ToolResult  → ToolName + ToolResult
//   - Done        → Content 是完整最终回答（用于持久化）
//   - Error       → Error
type AgentEvent struct {
	Type       AgentEventType
	Content    string
	ToolName   string
	ToolLabel  string
	ToolResult string
	Error      error
}
