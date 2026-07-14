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

// 步骤状态（v1.5.5）。值与 conversation.StepStatus* 保持一致，handler 直接透传。
const (
	StepStatusSuccess  = "success"   // 执行成功
	StepStatusError    = "error"     // 执行返回 error
	StepStatusNotFound = "not_found" // 工具不存在
)

const (
	// AgentEventToken：LLM 吐出的文本 token 增量。思考轮和回复轮的 token 都走这个事件--
	// 区分靠轮末的 AgentEventFinal(回复轮)或 AgentEventToolCall(思考轮出现工具)。
	// 前端按字符级拼接渲染,收到 Final/ToolCall 时给当前段定性。
	AgentEventToken AgentEventType = "token"

	// AgentEventToolCall：LLM 决定调用某个工具，准备执行（用户友好的「正在调用 xxx」状态条）。
	// 它的出现也标志当前轮是思考轮(有工具调用)。
	AgentEventToolCall AgentEventType = "tool_call"

	// AgentEventToolResult：工具执行完成，附带原始结果。前端默认不展示，留作调试或后续展开 UI。
	AgentEventToolResult AgentEventType = "tool_result"

	// AgentEventLLMCall：一次 LLM 调用完成（v1.5.5）。携带该轮发出的请求与模型回复，
	// 供运行链路记录（agent_steps）。不推给前端，仅 handler 消费落库。
	AgentEventLLMCall AgentEventType = "llm_call"

	// AgentEventFinal：回复轮标记(思考模式改造,C5)。RunStream 在每个「无 tool_call 的轮」
	// 末发出,Content 携带该轮完整文本(= 最终回复)。前端/IM 据此明确区分思考与回复,
	// 不再靠「最后一个 text 段」推断。异常结束(超时/maxSteps)也发 Final(兜底文案),
	// 保证消费方总能收到一次 Final;LLM 调用失败走 Error 不发 Final。
	AgentEventFinal AgentEventType = "final"

	// AgentEventDone：整个 ReAct 循环结束的终止信号(在 Final 之后发出)。Content 字段
	// 携带兜底文案,仅在消费方未收到 Final 时作兜底用。
	AgentEventDone AgentEventType = "done"

	// AgentEventError：流式过程中发生错误，channel 即将关闭。
	AgentEventError AgentEventType = "error"
)

// AgentEvent 是 ReActAgent.RunStream 推给上游的高层事件。
// 字段语义按 Type 区分：
//   - Token       → Content 是 token 增量
//   - ToolCall    → ToolName + ToolLabel
//   - ToolResult  → ToolName + ToolResult（原始未脱敏）+ ToolArguments + StepStatus + StepDurationMs
//   - LLMCall     → LLMRequest + LLMResponse + StepStatus + StepDurationMs（运行链路记录，v1.5.5）
//   - Final       -> Content 是回复轮完整文本(最终回复),思考模式 C5 标记
//   - Done        -> Content 是兜底文案(仅在未收到 Final 时使用)
//   - Error       → Error
//
// ToolResult 事件的 ToolResult 字段携带**原始未脱敏**结果（含真实错误），供 handler 落
// agent_steps 记录表；对外展示时由 handler 单独脱敏（sanitizeToolResult）。
// StepStatus / StepDurationMs 是工具与 LLM 步骤共用的通用字段（v1.5.5）。
type AgentEvent struct {
	Type       AgentEventType
	Content    string
	ToolName   string
	ToolLabel  string
	ToolResult string
	// 工具与 LLM 步骤共用的记录字段（v1.5.5）
	ToolArguments  string // 工具步骤：原始 arguments JSON
	StepStatus     string // success/error/not_found
	StepDurationMs int64  // 本步耗时（毫秒）
	// LLM 调用步骤专用（v1.5.5）
	LLMRequest  string // 该轮发出的 messages JSON 快照
	LLMResponse string // 模型回复 {content, tool_calls} JSON
	Error       error
}
