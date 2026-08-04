package agent

// ToolCallBudget 工具调用预算默认值。达此阈值后移除所有工具,强制 LLM 基于已收集信息出报告。
// 与 researcher prompt "5 来源上限"对齐(略宽,允许少量重试:每次 web_read 抓一 URL 算一次,
// 8 次约对应 5-6 个来源)。可按 card 覆盖。
const ToolCallBudget = 8

// ToolBudgetHook 工具调用总数预算 hook(P2 硬约束)。
//
// 解决子 Agent 超时(task39/40/42):LLM 反复换 URL 抓取,prompt 软约束"5 来源上限"不被遵守。
// 本 hook 硬约束:统计工具调用总数,达阈值后 BeforeRound 返回空工具集(hookChain 交集语义 ->
// 所有工具消失),LLM 无工具可用只能出文本报告。
//
// 与 CircuitBreakerHook 互补:熔断移除"个别失败工具",预算移除"所有工具"(达总量强制收尾)。
// 计数在 Runtime.ToolCallCount(循环每次工具执行后 ++),hook 只读不拦。
type ToolBudgetHook struct {
	budget int
}

// NewToolBudgetHook 创建工具预算 hook。budget=工具调用总数上限。
func NewToolBudgetHook(budget int) *ToolBudgetHook {
	return &ToolBudgetHook{budget: budget}
}

// BeforeRound 达预算返回空集(移除所有工具,强制出文本);未达返回全量。
// hookChain 取交集:空集与其他 hook 结果取交集仍为空 -> 所有工具消失。
func (h *ToolBudgetHook) BeforeRound(rt *Runtime) []map[string]interface{} {
	if rt.ToolCallCount >= h.budget {
		return []map[string]interface{}{} // 空集:强制 LLM 无工具可用
	}
	return rt.Tools
}

// OnToolExecute 不拦截:计数由 RunStream 循环负责(ToolCallCount++),hook 只读。
func (h *ToolBudgetHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	return "", "", false
}

// OnLLMResult 预算不关心 LLM 结果,放行。
func (h *ToolBudgetHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	return true
}

// OnMaxExhausted 预算不处理 MaxSteps 兜底(交 ForceSummaryHook)。
func (h *ToolBudgetHook) OnMaxExhausted(rt *Runtime) string { return "" }
