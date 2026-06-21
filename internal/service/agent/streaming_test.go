package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStreamingLLMClient 模拟流式 LLM 客户端。每次 ChatCompletionStream 调用对应一轮 ReAct，
// 按预先脚本好的 chunks 推到一个 channel，然后关闭 channel。多轮场景由调用方调多次。
type mockStreamingLLMClient struct {
	rounds    [][]LLMStreamChunk // 每轮一个 chunks 序列
	callCount int
	openErr   error // 用于模拟 stream 打开失败
}

func (m *mockStreamingLLMClient) ChatCompletionStream(
	ctx context.Context,
	messages []map[string]interface{},
	tools []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	if m.openErr != nil {
		return nil, m.openErr
	}
	if m.callCount >= len(m.rounds) {
		// 安全兜底：超出预设轮数返回空流（让 agent 走 fallback）
		ch := make(chan LLMStreamChunk)
		close(ch)
		m.callCount++
		return ch, nil
	}
	chunks := m.rounds[m.callCount]
	m.callCount++

	ch := make(chan LLMStreamChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// drainEvents 从 AgentEvent channel 收集所有事件直到关闭，便于断言。
func drainEvents(t *testing.T, ch <-chan AgentEvent) []AgentEvent {
	t.Helper()
	var events []AgentEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, e)
		case <-timeout:
			t.Fatal("timed out waiting for AgentEvent channel close")
			return events
		}
	}
}

// TestReActAgent_RunStream_NoToolCall：LLM 直接给文本回答（无工具调用），
// agent 应原样把每个 token 转发出去，最后 emit Done 携带完整文本。
func TestReActAgent_RunStream_NoToolCall(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ContentDelta: "你"},
				{ContentDelta: "好"},
				{ContentDelta: "！"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{}, // 同步接口不会被流式路径调用，仅满足 config 必填
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "你好"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 预期：3 个 Token + 1 个 Done
	require.Len(t, events, 4)
	assert.Equal(t, AgentEventToken, events[0].Type)
	assert.Equal(t, "你", events[0].Content)
	assert.Equal(t, "好", events[1].Content)
	assert.Equal(t, "！", events[2].Content)
	assert.Equal(t, AgentEventDone, events[3].Type)
	assert.Equal(t, "你好！", events[3].Content)
}

// TestReActAgent_RunStream_SingleToolCall：LLM 第一轮决定调用一个工具，第二轮输出文本回答。
// agent 应 emit ToolCall + ToolResult，然后流式 token，最后 Done。
func TestReActAgent_RunStream_SingleToolCall(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 第一轮：tool_call delta 跨多个 chunk
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "call_1", Name: "get_current_time"}},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// 第二轮：纯文本回答
			{
				{ContentDelta: "现在是 "},
				{ContentDelta: "10:30"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:         "get_current_time",
		Description:  "Get current time",
		DisplayLabel: "查询了当前时间",
		Parameters:   map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "2026-06-17 10:30:00 CST", nil
		},
	})
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "现在几点？"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 预期事件序列：ToolCall、ToolResult、Token、Token、Done
	require.GreaterOrEqual(t, len(events), 5)
	assert.Equal(t, AgentEventToolCall, events[0].Type)
	assert.Equal(t, "get_current_time", events[0].ToolName)
	assert.Equal(t, "查询了当前时间", events[0].ToolLabel)

	assert.Equal(t, AgentEventToolResult, events[1].Type)
	assert.Equal(t, "get_current_time", events[1].ToolName)
	assert.Equal(t, "2026-06-17 10:30:00 CST", events[1].ToolResult)

	// 后续是 token 流和 done
	assert.Equal(t, AgentEventToken, events[2].Type)
	assert.Equal(t, "现在是 ", events[2].Content)
	assert.Equal(t, AgentEventToken, events[3].Type)
	assert.Equal(t, "10:30", events[3].Content)
	assert.Equal(t, AgentEventDone, events[4].Type)
	assert.Equal(t, "现在是 10:30", events[4].Content)
}

// TestReActAgent_RunStream_MultipleToolCalls：LLM 连续调用两次工具再给最终回答。
// 验证 ReAct 循环能跨轮串联，事件按时序输出。
func TestReActAgent_RunStream_MultipleToolCalls(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "tool_a", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c2", Name: "tool_b", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "完成"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name: "tool_a", DisplayLabel: "执行了工具A",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "result_a", nil
		},
	})
	registry.Register(Tool{
		Name: "tool_b", DisplayLabel: "执行了工具B",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "result_b", nil
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "ab"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 收集事件类型序列
	types := make([]AgentEventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}

	expected := []AgentEventType{
		AgentEventToolCall, AgentEventToolResult, // tool_a
		AgentEventToolCall, AgentEventToolResult, // tool_b
		AgentEventToken, // "完成"
		AgentEventDone,
	}
	assert.Equal(t, expected, types)

	// 验证两次工具调用顺序正确
	assert.Equal(t, "tool_a", events[0].ToolName)
	assert.Equal(t, "tool_b", events[2].ToolName)
	assert.Equal(t, "完成", events[len(events)-1].Content)
}

// TestReActAgent_RunStream_ToolNotFound：LLM 调用了一个未注册的工具，
// agent 应把错误结果作为 tool_result 塞回继续推理，不应崩溃。
func TestReActAgent_RunStream_ToolNotFound(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "nonexistent", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "抱歉"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "x"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 第一个事件应该是 ToolCall（即使工具不存在，LLM 决定调就先 emit 给前端）
	require.NotEmpty(t, events)
	assert.Equal(t, AgentEventToolCall, events[0].Type)
	assert.Equal(t, "nonexistent", events[0].ToolName)

	// 第二个事件 ToolResult 应该携带"工具不存在"的错误信息
	require.GreaterOrEqual(t, len(events), 2)
	assert.Equal(t, AgentEventToolResult, events[1].Type)
	assert.Contains(t, events[1].ToolResult, "nonexistent")

	// 最后应正常以 Done 结束
	assert.Equal(t, AgentEventDone, events[len(events)-1].Type)
}

// noopSyncLLM 仅用于满足 ReActAgentConfig.LLMClient 必填，流式路径不会调用它。
type noopSyncLLM struct{}

func (noopSyncLLM) ChatCompletion(
	ctx context.Context,
	messages []map[string]interface{},
	tools []map[string]interface{},
) (string, []map[string]interface{}, error) {
	return "", nil, nil
}
