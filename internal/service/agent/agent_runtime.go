package agent

import "context"

// Runtime 是 ReAct 循环的运行时共享状态,贯穿整个 RunStream,所有 hook 通过它交互。
// hook 只读写 Runtime,不直接碰循环的局部变量(边界清晰)。
type Runtime struct {
	Ctx         context.Context            // 请求 ctx(供 hook 调 LLM 用,如强制汇总)
	Messages    []map[string]interface{}   // 对话历史(可读写:工具结果会 append)
	Tools       []map[string]interface{}   // 全量工具(原始,ToOpenAITools 产物,不变)
	FinalAnswer string                     // 累积最终回答(回复轮文本累加)
	FailStreak  map[string]int             // 工具连续失败计数(熔断状态,hook 维护)
	Emit        func(AgentEvent)           // 事件出口(emit 到 out channel)
	Step        int                        // 当前轮次(1-based)
}

// RoundHook 是 ReAct 循环的可插拔扩展点。多个 hook 串成链,在循环的固定切点被调用。
//
// 设计目的:把熔断、强制汇总等横切机制从 RunStream 主循环里抽离,使循环保持纯推理,
// 机制可独立测试、按需装配(主 Agent / 子 Agent 差异化)、热插拔(新机制=新 hook,不动循环)。
//
// 各切点的调用规则:
//   - BeforeRound:链式 -- 每个 hook 在上一个输出上再过滤 tools,最后结果用于本轮调 LLM
//   - OnToolExecute:短路 -- 链上第一个返回 executed=true 即止(如熔断拦截),否则真正执行工具
//   - OnLLMResult:广播 -- 每个 hook 都调(预留思考模式/审计;当前无内置实现)
//   - OnMaxExhausted:广播 -- 每个 hook 都调,取第一个非空返回作为汇总文本
type RoundHook interface {
	// BeforeRound 每轮开始:过滤本轮可用 tools(如熔断移除已禁工具)。返回本轮发给 LLM 的 tools。
	BeforeRound(rt *Runtime) []map[string]interface{}

	// OnLLMResult LLM 流结束、拿到本轮文本+tool_calls 后调用。
	// 返回 proceed=false 表示不再执行本轮工具(预留:外部强制终止)。
	OnLLMResult(rt *Runtime, content, reasoning string, toolCalls []ToolCall) (proceed bool)

	// OnToolExecute 执行单个工具前。返回 executed=true 表示已处理(如熔断拦截),不真正 Execute。
	// 链上第一个 executed=true 即短路;都未拦截才真正执行工具。
	OnToolExecute(rt *Runtime, call ToolCall) (result, status string, executed bool)

	// OnMaxExhausted 达 MaxSteps 时调用。返回汇总文本(强制汇总);空串由调用方回落兜底文案。
	OnMaxExhausted(rt *Runtime) string
}

// noopRoundHook 无操作的占位 hook(链为空时用,避免 nil 判断)。
type noopRoundHook struct{}

func (noopRoundHook) BeforeRound(rt *Runtime) []map[string]interface{} { return rt.Tools }
func (noopRoundHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	return true
}
func (noopRoundHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	return "", "", false // 不拦截,交循环真正执行
}
func (noopRoundHook) OnMaxExhausted(rt *Runtime) string { return "" }

// hookChain 把多个 RoundHook 串成链,实现 RoundHook 接口(组合模式)。
// 按各切点的规则聚合结果。
type hookChain struct {
	hooks []RoundHook
}

// newHookChain 构造 hook 链。nil/空切片返回 noop(纯推理,无机制)。
func newHookChain(hooks []RoundHook) RoundHook {
	if len(hooks) == 0 {
		return noopRoundHook{}
	}
	return &hookChain{hooks: hooks}
}

// BeforeRound 链式:每个 hook 在上一个输出上再过滤。
func (c *hookChain) BeforeRound(rt *Runtime) []map[string]interface{} {
	tools := rt.Tools
	for _, h := range c.hooks {
		// 把当前过滤结果放回 rt(供下一个 hook 读取一致)
		rt.Tools = tools
		tools = h.BeforeRound(rt)
	}
	return tools
}

// OnLLMResult 广播:每个 hook 都调,任一返回 proceed=false 即停止。
func (c *hookChain) OnLLMResult(rt *Runtime, content, reasoning string, toolCalls []ToolCall) bool {
	for _, h := range c.hooks {
		if !h.OnLLMResult(rt, content, reasoning, toolCalls) {
			return false
		}
	}
	return true
}

// OnToolExecute 短路:第一个 executed=true 即止。
func (c *hookChain) OnToolExecute(rt *Runtime, call ToolCall) (string, string, bool) {
	for _, h := range c.hooks {
		result, status, executed := h.OnToolExecute(rt, call)
		if executed {
			return result, status, true
		}
	}
	return "", "", false
}

// OnMaxExhausted 广播:取第一个非空返回。
func (c *hookChain) OnMaxExhausted(rt *Runtime) string {
	for _, h := range c.hooks {
		if s := h.OnMaxExhausted(rt); s != "" {
			return s
		}
	}
	return ""
}
