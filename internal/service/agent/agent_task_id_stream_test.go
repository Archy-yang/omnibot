package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReActAgent_RunStream_EmitsTaskCreatedEvent 调了 delegate 后:
// 应 emit 独立的 AgentEventTaskCreated(携带 task_id),而 Final 回复文本不再拼接"任务ID"。
func TestReActAgent_RunStream_EmitsTaskCreatedEvent(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 第一轮:调 delegate
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "call_1", Name: "delegate"}},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ArgumentsDelta: `{"sub_agent_type":"researcher","goal":"查高铁"}`}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// 第二轮:纯文本回复
			{
				{ContentDelta: "已经安排研究员去查高铁票了"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "delegate",
		Description: "派活",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"task_id": 43, "status": "pending", "message": "已安排子 Agent 处理,稍后汇报"}`, nil
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
		{"role": "user", "content": "帮我查高铁票"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	var finalContent string
	var created []int64
	for _, e := range events {
		switch e.Type {
		case AgentEventFinal:
			finalContent = e.Content
		case AgentEventTaskCreated:
			created = e.TaskIDs
		}
	}
	require.NotEmpty(t, finalContent)
	assert.Contains(t, finalContent, "已经安排研究员去查高铁票了")
	assert.NotContains(t, finalContent, "任务ID", "任务ID 不应再拼进回复文本(独立事件下发)")
	require.Len(t, created, 1, "调了 delegate 应 emit TaskCreated 事件")
	assert.Equal(t, int64(43), created[0])
}

// TestReActAgent_RunStream_NoTaskCreatedWhenNoDelegate 没调 delegate(直接回复):
// 不应 emit TaskCreated 事件,回复文本也不含"任务ID"。
func TestReActAgent_RunStream_NoTaskCreatedWhenNoDelegate(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 直接回复(无 tool_call)
			{
				{ContentDelta: "已经安排研究员去查高铁票了"},
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
		{"role": "user", "content": "帮我查高铁票"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	var finalContent string
	var sawTaskCreated bool
	for _, e := range events {
		if e.Type == AgentEventFinal {
			finalContent = e.Content
		}
		if e.Type == AgentEventTaskCreated {
			sawTaskCreated = true
		}
	}
	require.NotEmpty(t, finalContent)
	assert.Contains(t, finalContent, "已经安排研究员去查高铁票了")
	assert.NotContains(t, finalContent, "任务ID")
	assert.False(t, sawTaskCreated, "没调 delegate 不应 emit TaskCreated 事件")
}
