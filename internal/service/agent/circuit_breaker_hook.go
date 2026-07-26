package agent

import "fmt"

// CircuitBreakerHook 工具连续失败熔断 hook(B)。
//
// 吸收原散落在 RunStream 循环里的熔断逻辑(见 task 18:web_reader 对 401 站点连失败,
// 模型仍反复调)。两处切点:
//   - BeforeRound:从本轮 tools 移除已达阈值的工具(模型看不到,无法发起调用 -- 硬约束)
//   - OnToolExecute:达阈值的工具调用直接拦截,返回禁用提示(双保险,防模型凭旧上下文再调)
//
// FailStreak 计数在 Runtime 上共享。循环在工具执行后调 UpdateStreak 更新(成功清零/失败++)。
type CircuitBreakerHook struct {
	threshold int
}

// NewCircuitBreakerHook 创建熔断 hook。threshold=连续失败几次后熔断。
func NewCircuitBreakerHook(threshold int) *CircuitBreakerHook {
	return &CircuitBreakerHook{threshold: threshold}
}

// BeforeRound 移除已达阈值的工具,让模型看不到它们(硬约束)。
func (h *CircuitBreakerHook) BeforeRound(rt *Runtime) []map[string]interface{} {
	return filterToolsByCircuitBreaker(rt.Tools, rt.FailStreak, h.threshold)
}

// OnToolExecute 达阈值的工具拦截:不执行,返回禁用提示。
// 未达阈值返回 executed=false 交循环真正执行(执行后循环调 UpdateStreak 更新计数)。
func (h *CircuitBreakerHook) OnToolExecute(rt *Runtime, call ToolCall) (string, string, bool) {
	if rt.FailStreak[call.Name] >= h.threshold {
		msg := fmt.Sprintf("工具 %q 已连续失败 %d 次,已禁用。请改用其他来源或基于已有信息汇总,不要再调用该工具。",
			call.Name, rt.FailStreak[call.Name])
		return msg, StepStatusError, true // 拦截
	}
	return "", "", false // 不拦截
}

// OnLLMResult 熔断不关心 LLM 结果,直接放行。
func (h *CircuitBreakerHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	return true
}

// OnMaxExhausted 熔断不处理 MaxSteps 兜底(交 ForceSummaryHook)。
func (h *CircuitBreakerHook) OnMaxExhausted(rt *Runtime) string { return "" }

// UpdateStreak 工具执行后更新连续失败计数。success=true 清零,否则++。
// 由 ReAct 循环在真正执行工具(OnToolExecute 未拦截)后调用。
func (h *CircuitBreakerHook) UpdateStreak(rt *Runtime, toolName string, success bool) {
	if rt.FailStreak == nil {
		rt.FailStreak = make(map[string]int)
	}
	if success {
		rt.FailStreak[toolName] = 0 // 成功清零,偶尔失败不误熔断
	} else {
		rt.FailStreak[toolName]++
	}
}
