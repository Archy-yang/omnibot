package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkTool 构造 OpenAI tools 格式的测试工具条目。
func mkTool(name string) map[string]interface{} {
	return map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{"name": name},
	}
}

// toolName 取 tool 条目的 name。
func toolName(t map[string]interface{}) string {
	fn, _ := t["function"].(map[string]interface{})
	n, _ := fn["name"].(string)
	return n
}

// TestCircuitBreakerHook_BeforeRound_RemovesTrippedTools BeforeRound 应移除已达熔断阈值的工具。
func TestCircuitBreakerHook_BeforeRound_RemovesTrippedTools(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	tools := []map[string]interface{}{mkTool("a"), mkTool("b"), mkTool("c")}

	// a 失败 3 次达阈值
	rt := &Runtime{Tools: tools, FailStreak: map[string]int{"a": 3}}
	got := h.BeforeRound(rt)

	assert.Len(t, got, 2, "a 已熔断应被移除")
	assert.NotContains(t, []string{toolName(got[0]), toolName(got[1])}, "a")
}

// TestCircuitBreakerHook_BeforeRound_NoneTripped 没达阈值的工具保留。
func TestCircuitBreakerHook_BeforeRound_NoneTripped(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	tools := []map[string]interface{}{mkTool("a"), mkTool("b")}
	rt := &Runtime{Tools: tools, FailStreak: map[string]int{"a": 2}} // 没到 3
	got := h.BeforeRound(rt)
	assert.Len(t, got, 2, "都没达阈值,原样返回")
}

// TestCircuitBreakerHook_OnToolExecute_TrippedIntercepts 达阈值的工具被拦截(不执行),
// 返回禁用提示,executed=true。
func TestCircuitBreakerHook_OnToolExecute_TrippedIntercepts(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	rt := &Runtime{FailStreak: map[string]int{"web_reader": 3}}

	r, st, exec := h.OnToolExecute(rt, ToolCall{Name: "web_reader"})
	assert.True(t, exec, "达阈值应拦截")
	assert.Equal(t, StepStatusError, st)
	assert.Contains(t, r, "已连续失败")
	assert.Contains(t, r, "已禁用")
	assert.Contains(t, r, "web_reader")
}

// TestCircuitBreakerHook_OnToolExecute_NotTrippedPassesThrough 未达阈值不拦截(executed=false)交循环执行。
func TestCircuitBreakerHook_OnToolExecute_NotTrippedPassesThrough(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	rt := &Runtime{FailStreak: map[string]int{"web_reader": 2}} // 没到 3

	_, _, exec := h.OnToolExecute(rt, ToolCall{Name: "web_reader"})
	assert.False(t, exec, "未达阈值不拦截")
}

// CircuitBreakerHook 不自己更新 FailStreak(那是循环 Execute 后的职责),
// 但需提供 UpdateStreak 让循环在工具执行后更新计数。这里测 UpdateStreak 的成功/失败/清零。
func TestCircuitBreakerHook_UpdateStreak(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	rt := &Runtime{FailStreak: map[string]int{}}

	h.UpdateStreak(rt, "web_reader", false) // 失败
	assert.Equal(t, 1, rt.FailStreak["web_reader"])
	h.UpdateStreak(rt, "web_reader", false)
	assert.Equal(t, 2, rt.FailStreak["web_reader"])
	h.UpdateStreak(rt, "web_reader", true) // 成功清零
	assert.Equal(t, 0, rt.FailStreak["web_reader"])
}

// TestCircuitBreakerHook_AllTrippedReturnsEmpty 全部工具熔断时 BeforeRound 返回空,
// 模型无工具可用(与强制汇总呼应)。
func TestCircuitBreakerHook_AllTrippedReturnsEmpty(t *testing.T) {
	h := NewCircuitBreakerHook(3)
	tools := []map[string]interface{}{mkTool("a"), mkTool("b")}
	rt := &Runtime{Tools: tools, FailStreak: map[string]int{"a": 3, "b": 3}}
	got := h.BeforeRound(rt)
	assert.Empty(t, got, "全部熔断应返回空")
}

// require 用于断言 no panic
var _ = require.NoError
var _ = fmt.Sprintf
var _ = context.Background
