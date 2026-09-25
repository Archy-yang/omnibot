package agent

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Control Signal holder 单测(Phase 6,16-路线图 §13) ----

// TestControlSignal_Holder ctx 携带信号 holder:工具上报,运行时读取。
func TestControlSignal_Holder(t *testing.T) {
	// 未注入 holder:读取返回空,不 panic
	assert.Empty(t, PeekControlSignal(context.Background()))

	ctx := WithControlSignal(context.Background())
	assert.Empty(t, PeekControlSignal(ctx), "初始无信号")

	RaiseControlSignal(ctx, ControlSuspend)
	assert.Equal(t, ControlSuspend, PeekControlSignal(ctx))

	// 信号粘滞:首个信号优先,后续上报不得覆盖(Suspend 不被改写)
	RaiseControlSignal(ctx, ControlStop)
	assert.Equal(t, ControlSuspend, PeekControlSignal(ctx))
}

// ---- ReAct 循环消费 Suspend 集成测试 ----

// TestReActAgent_RunStream_SuspendStopsLoop 工具上报 Suspend 后:
// 同批后续工具不再执行、不进入下一轮 ReAct、直接产出 Final/Done(§13)。
func TestReActAgent_RunStream_SuspendStopsLoop(t *testing.T) {
	var toolBExecuted atomic.Bool
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "tool_suspend", ArgumentsDelta: "{}"}},
				{ToolCallDelta: &ToolCallDelta{Index: 1, ID: "c2", Name: "tool_after", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// 若错误地进入第二轮,会走这段:纯文本(让"没停"的场景自证)
			{
				{ContentDelta: "不该出现"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	require.NoError(t, registry.Register(Tool{
		Name:        "tool_suspend",
		Description: "raises suspend",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			RaiseControlSignal(ctx, ControlSuspend)
			return "已请求输入", nil
		},
	}))
	require.NoError(t, registry.Register(Tool{
		Name:        "tool_after",
		Description: "must not run after suspend",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			toolBExecuted.Store(true)
			return "b", nil
		},
	}))
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ctx := WithControlSignal(context.Background())
	ch, err := agent.RunStream(ctx, []map[string]interface{}{{"role": "user", "content": "x"}})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	assert.False(t, toolBExecuted.Load(), "Suspend 后同批后续工具不得执行")
	assert.Equal(t, 1, llm.callCount, "Suspend 后不得进入下一轮 ReAct")

	// 事件流:ToolCall(c1)+ToolResult(c1)+ToolCall(c2)+ToolResult(c2 占位)+Final+Done
	var finals, dones []AgentEvent
	for _, e := range events {
		switch e.Type {
		case AgentEventFinal:
			finals = append(finals, e)
		case AgentEventDone:
			dones = append(dones, e)
		}
	}
	require.Len(t, finals, 1, "Suspend 应直接产出 Final")
	require.Len(t, dones, 1)
	assert.NotEqual(t, "不该出现", finals[0].Content, "Final 不得来自第二轮 LLM")
}
