package agent

// StepRecord 一轮 ReAct 的一步记录(llm_call / tool_call),供上层落 agent_steps 复盘。
// 它是 RunStream 流式事件聚合后的产物:同步 Run 把 AgentEvent 序列折叠成 []StepRecord 返回,
// 上层(web / 飞书 handler)再把它转成 conversation.AgentStep 落库——和流式 handler 同款数据。
//
// agent 包内部刻意不依赖 conversation domain,保持包独立;model 字段由上层(handler)用
// userConfig.Model / 默认 provider model 补全,与流式路径一致(LLMClient 接口未暴露模型名)。
type StepRecord struct {
	Kind       string // "llm_call" | "tool_call",见 StepKind* 常量
	Status     string // success / error / not_found,见 StepStatus* 常量(复用 streaming.go)
	DurationMs int64
	Tool       string // tool_call 用:工具名
	Request    string // llm: 该轮发出的 messages JSON 快照;tool: arguments JSON
	Response   string // llm: {content, tool_calls} JSON;tool: 原始未脱敏 raw result
}

// 步骤类型常量(v1.6)。值与 conversation.StepKindLLMCall/ToolCall 保持一致,
// handler 转换 conversation.AgentStep 时直接透传。
const (
	StepKindLLMCall  = "llm_call"
	StepKindToolCall = "tool_call"
)
