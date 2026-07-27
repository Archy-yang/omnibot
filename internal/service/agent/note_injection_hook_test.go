package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// TestNoteInjectionHook_BeforeRound_InjectsNewNotes 新 notes 追加 user 消息到 Messages。
// 已注入的 note 不重复注入。
func TestNoteInjectionHook_BeforeRound_InjectsNewNotes(t *testing.T) {
	hook := NewNoteInjectionHook(1, nil) // taskRepo=nil,用注入的 notes 模拟(测试直接设 hook.injectedCount)

	rt := &Runtime{
		Messages: []map[string]interface{}{
			{"role": "system", "content": "sys"},
			{"role": "user", "content": "原始 goal"},
		},
	}
	// 模拟:task 有 2 条 notes,hook 还没注入
	hook.setNotesForTest([]string{"补充1", "补充2"})

	hook.BeforeRound(rt)

	// 应追加 2 条 user 消息(补充1 + 补充2)
	require.GreaterOrEqual(t, len(rt.Messages), 4)
	last2 := rt.Messages[len(rt.Messages)-2:]
	assert.Equal(t, "user", last2[0]["role"])
	assert.Contains(t, last2[0]["content"].(string), "补充1")
	assert.Contains(t, last2[1]["content"].(string), "补充2")
}

// TestNoteInjectionHook_BeforeRound_NoNewNotes 不重复注入已注入的 notes。
func TestNoteInjectionHook_BeforeRound_NoDuplicate(t *testing.T) {
	hook := NewNoteInjectionHook(1, nil)
	hook.setNotesForTest([]string{"补充1"})

	rt := &Runtime{
		Messages: []map[string]interface{}{{"role": "user", "content": "g"}},
	}
	hook.BeforeRound(rt) // 注入"补充1"
	before := len(rt.Messages)

	hook.BeforeRound(rt) // 再次调用,notes 没新增,不应再注入
	assert.Equal(t, before, len(rt.Messages), "已注入的 note 不应重复注入")
}

// TestNoteInjectionHook_BeforeRound_NoNotes 无 notes 时不动 Messages。
func TestNoteInjectionHook_BeforeRound_NoNotes(t *testing.T) {
	hook := NewNoteInjectionHook(1, nil)
	hook.setNotesForTest(nil)
	rt := &Runtime{Messages: []map[string]interface{}{{"role": "user", "content": "g"}}}
	hook.BeforeRound(rt)
	assert.Len(t, rt.Messages, 1)
}

// TestNoteInjectionHook_OtherHooksPassThrough 其他切点不干预(放行)。
func TestNoteInjectionHook_OtherHooksPassThrough(t *testing.T) {
	hook := NewNoteInjectionHook(1, nil)
	rt := &Runtime{Tools: []map[string]interface{}{{"x": 1}}}
	assert.Equal(t, rt.Tools, hook.BeforeRound(rt), "BeforeRound 返回 tools 不该被改(本测试用空 notes)")
	// OnToolExecute 不拦截
	_, _, exec := hook.OnToolExecute(rt, ToolCall{})
	assert.False(t, exec)
	// OnLLMResult 放行
	assert.True(t, hook.OnLLMResult(rt, "", "", nil))
	// OnMaxExhausted 不产出
	assert.Empty(t, hook.OnMaxExhausted(rt))
}

var _ = domainagent.TaskStatusPending
