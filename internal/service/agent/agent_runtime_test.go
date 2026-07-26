package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// stubHook 可记录调用、控制返回值的测试 hook。
type stubHook struct {
	beforeTools []map[string]interface{} // BeforeRound 返回(链式:替换输入)
	toolResult string                     // OnToolExecute 返回(若 executed=true)
	toolExec   bool                       // OnToolExecute 是否拦截
	summary    string                     // OnMaxExhausted 返回
	proceed    bool                       // OnLLMResult 返回

	beforeCalled   int
	onLLMCalled    int
	onToolCalled   int
	onMaxCalled    int
}

func (s *stubHook) BeforeRound(rt *Runtime) []map[string]interface{} {
	s.beforeCalled++
	if s.beforeTools != nil {
		return s.beforeTools
	}
	return rt.Tools
}
func (s *stubHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	s.onLLMCalled++
	return s.proceed
}
func (s *stubHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	s.onToolCalled++
	return s.toolResult, StepStatusError, s.toolExec
}
func (s *stubHook) OnMaxExhausted(rt *Runtime) string {
	s.onMaxCalled++
	return s.summary
}

// TestHookChain_NoopEmpty 空 hook 链退化为 noop:BeforeRound 原样返回,OnToolExecute 不拦截,OnMax 空。
func TestHookChain_NoopEmpty(t *testing.T) {
	c := newHookChain(nil)
	rt := &Runtime{Tools: []map[string]interface{}{{"x": 1}}}
	assert.Equal(t, rt.Tools, c.BeforeRound(rt), "空链 BeforeRound 原样返回")

	r, st, exec := c.OnToolExecute(rt, ToolCall{Name: "x"})
	assert.False(t, exec, "空链不拦截工具")
	assert.Empty(t, r)
	assert.Empty(t, st)

	assert.Empty(t, c.OnMaxExhausted(rt), "空链 OnMax 返回空")
}

// TestHookChain_BeforeRound_Chained BeforeRound 链式:每个 hook 在上一个结果上再过滤。
func TestHookChain_BeforeRound_Chained(t *testing.T) {
	all := []map[string]interface{}{
		{"type": "function", "function": map[string]interface{}{"name": "a"}},
		{"type": "function", "function": map[string]interface{}{"name": "b"}},
		{"type": "function", "function": map[string]interface{}{"name": "c"}},
	}
	// hook1 移除 a,hook2 移除 b -> 只剩 c
	hook1 := &stubHook{beforeTools: filterByName(all, "a")}
	hook2 := &stubHook{beforeTools: filterByName(filterByName(all, "a"), "b")}
	c := newHookChain([]RoundHook{hook1, hook2})

	rt := &Runtime{Tools: all}
	got := c.BeforeRound(rt)
	assert.Len(t, got, 1, "两个 hook 链式过滤后只剩 c")
	fn := got[0]["function"].(map[string]interface{})
	assert.Equal(t, "c", fn["name"])
}

// TestHookChain_OnToolExecute_ShortCircuit OnToolExecute 短路:第一个 executed=true 即止,后续 hook 不调。
func TestHookChain_OnToolExecute_ShortCircuit(t *testing.T) {
	hook1 := &stubHook{toolResult: "拦截了", toolExec: true}
	hook2 := &stubHook{toolExec: true}
	c := newHookChain([]RoundHook{hook1, hook2})

	r, st, exec := c.OnToolExecute(&Runtime{}, ToolCall{})
	assert.True(t, exec)
	assert.Equal(t, "拦截了", r)
	assert.Equal(t, StepStatusError, st)
	assert.Equal(t, 1, hook1.onToolCalled, "hook1 应被调用")
	assert.Equal(t, 0, hook2.onToolCalled, "hook1 短路后 hook2 不应被调用")
}

// TestHookChain_OnToolExecute_NoIntercept 所有 hook 都不拦截时,返回 executed=false 交循环执行。
func TestHookChain_OnToolExecute_NoIntercept(t *testing.T) {
	hook1 := &stubHook{toolExec: false}
	hook2 := &stubHook{toolExec: false}
	c := newHookChain([]RoundHook{hook1, hook2})

	_, _, exec := c.OnToolExecute(&Runtime{}, ToolCall{})
	assert.False(t, exec, "都不拦截则交循环执行")
	assert.Equal(t, 1, hook1.onToolCalled)
	assert.Equal(t, 1, hook2.onToolCalled)
}

// TestHookChain_OnMaxExhausted_FirstNonEmpty OnMaxExhausted 取第一个非空返回。
func TestHookChain_OnMaxExhausted_FirstNonEmpty(t *testing.T) {
	hook1 := &stubHook{summary: ""}       // 空,跳过
	hook2 := &stubHook{summary: "汇总报告"} // 非空,取它
	hook3 := &stubHook{summary: "不应被取"}
	c := newHookChain([]RoundHook{hook1, hook2, hook3})

	got := c.OnMaxExhausted(&Runtime{})
	assert.Equal(t, "汇总报告", got)
	assert.Equal(t, 1, hook1.onMaxCalled)
	assert.Equal(t, 1, hook2.onMaxCalled)
	assert.Equal(t, 0, hook3.onMaxCalled, "hook2 非空后 hook3 不调")
}

// TestHookChain_OnLLMResult_Broadcast OnLLMResult 广播:每个都调,任一 false 即停止。
func TestHookChain_OnLLMResult_Broadcast(t *testing.T) {
	hook1 := &stubHook{proceed: true}
	hook2 := &stubHook{proceed: false} // 停止
	hook3 := &stubHook{proceed: true}
	c := newHookChain([]RoundHook{hook1, hook2, hook3})

	proceed := c.OnLLMResult(&Runtime{}, "c", "r", nil)
	assert.False(t, proceed)
	assert.Equal(t, 1, hook1.onLLMCalled)
	assert.Equal(t, 1, hook2.onLLMCalled)
	assert.Equal(t, 0, hook3.onLLMCalled, "hook2 返回 false 后 hook3 不调")
}

// filterByName 测试辅助:从 tools 移除指定 name。
func filterByName(tools []map[string]interface{}, removeName string) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name := ""
		if fn, ok := t["function"].(map[string]interface{}); ok {
			if n, ok := fn["name"].(string); ok {
				name = n
			}
		}
		if name == removeName {
			continue
		}
		out = append(out, t)
	}
	return out
}
