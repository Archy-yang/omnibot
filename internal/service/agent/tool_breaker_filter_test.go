package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFilterToolsByCircuitBreaker 熔断的工具应从本轮发给 LLM 的 tools 列表移除,
// 让模型根本看不到该工具(硬约束),而非只在调用时返回提示(软约束,模型仍会反复调)。
func TestFilterToolsByCircuitBreaker(t *testing.T) {
	tools := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{"name": "web_read", "description": "r"}},
		{"type": "function", "function": map[string]interface{}{"name": "calculator", "description": "f"}},
		{"type": "function", "function": map[string]interface{}{"name": "rss_reader", "description": "rss"}},
	}
	streak := map[string]int{"web_read": 3} // web_read 达熔断阈值

	got := filterToolsByCircuitBreaker(tools, streak, ToolFailureThreshold)

	// web_read 被移除,剩下 calculator + rss_reader
	assert.Len(t, got, 2)
	names := []string{}
	for _, t := range got {
		fn := t["function"].(map[string]interface{})
		names = append(names, fn["name"].(string))
	}
	assert.NotContains(t, names, "web_read")
	assert.Contains(t, names, "calculator")
	assert.Contains(t, names, "rss_reader")
}

// TestFilterToolsByCircuitBreaker_NoneTripped 没有工具达阈值时,原样返回全部。
func TestFilterToolsByCircuitBreaker_NoneTripped(t *testing.T) {
	tools := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{"name": "a"}},
		{"type": "function", "function": map[string]interface{}{"name": "b"}},
	}
	streak := map[string]int{"a": 1, "b": 2} // 都没到阈值 3

	got := filterToolsByCircuitBreaker(tools, streak, ToolFailureThreshold)
	assert.Len(t, got, 2, "都没达阈值,应原样返回")
}

// TestFilterToolsByCircuitBreaker_AllTripped 全部熔断时返回空(模型无工具可用,只能出文本)。
func TestFilterToolsByCircuitBreaker_AllTripped(t *testing.T) {
	tools := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{"name": "a"}},
		{"type": "function", "function": map[string]interface{}{"name": "b"}},
	}
	streak := map[string]int{"a": 3, "b": 3}

	got := filterToolsByCircuitBreaker(tools, streak, ToolFailureThreshold)
	assert.Empty(t, got, "全部熔断应返回空,模型无工具只能出报告")
}
