package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestToolBudgetHook_BeforeRoundUnderBudget 未达预算时返回全量工具(不过滤)。
func TestToolBudgetHook_BeforeRoundUnderBudget(t *testing.T) {
	h := NewToolBudgetHook(8)
	rt := &Runtime{
		Tools:         []map[string]interface{}{{"function": map[string]interface{}{"name": "web_read"}}},
		ToolCallCount: 5, // < 8
	}
	got := h.BeforeRound(rt)
	assert.Len(t, got, 1, "未达预算应原样返回全部工具")
}

// TestToolBudgetHook_BeforeRoundAtBudget 达预算时返回空集(移除所有工具,强制 LLM 出文本)。
func TestToolBudgetHook_BeforeRoundAtBudget(t *testing.T) {
	h := NewToolBudgetHook(8)
	rt := &Runtime{
		Tools:         []map[string]interface{}{{"function": map[string]interface{}{"name": "web_read"}}},
		ToolCallCount: 8, // == 预算
	}
	got := h.BeforeRound(rt)
	assert.Empty(t, got, "达预算应返回空集(移除所有工具)")
}

// TestToolBudgetHook_BeforeRoundOverBudget 超预算也返回空集(持续禁用)。
func TestToolBudgetHook_BeforeRoundOverBudget(t *testing.T) {
	h := NewToolBudgetHook(8)
	rt := &Runtime{
		Tools:         []map[string]interface{}{{"function": map[string]interface{}{"name": "web_read"}}},
		ToolCallCount: 10, // > 预算
	}
	got := h.BeforeRound(rt)
	assert.Empty(t, got, "超预算应持续返回空集")
}

// TestToolBudgetHook_OnToolExecute_NoIntercept 始终不拦截(计数在循环做,hook 只读)。
func TestToolBudgetHook_OnToolExecute_NoIntercept(t *testing.T) {
	h := NewToolBudgetHook(8)
	rt := &Runtime{ToolCallCount: 100}
	_, _, executed := h.OnToolExecute(rt, ToolCall{Name: "web_read"})
	assert.False(t, executed, "ToolBudget 不拦截工具执行,计数由循环负责")
}

// TestHookChain_ToolBudgetRemovesAllTools 链中含 ToolBudgetHook 达预算时,
// 交集为空(CircuitBreaker 移除个别,ToolBudget 移除全部,后者覆盖)。
func TestHookChain_ToolBudgetRemovesAllTools(t *testing.T) {
	chain := newHookChain([]RoundHook{
		NewCircuitBreakerHook(3), // 无熔断,保留全部
		NewToolBudgetHook(8),     // 达预算,返回空
	})
	rt := &Runtime{
		Tools:         []map[string]interface{}{{"function": map[string]interface{}{"name": "web_read"}}},
		FailStreak:    map[string]int{}, // 无熔断
		ToolCallCount: 8,                // 达预算
	}
	got := chain.BeforeRound(rt)
	assert.Empty(t, got, "ToolBudget 达预算时,链交集应为空(所有工具被移除)")
}
