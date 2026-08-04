package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReActAgent_RunStream_AppendsTaskIDToFinal 调了 delegate 后,
// 回复末尾应拼接"任务ID: 43"(程序追加,LLM 写不了)。
func TestReActAgent_RunStream_AppendsTaskIDToFinal(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 第一轮:调 delegate
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "call_1", Name: "delegate"}},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ArgumentsDelta: `{"sub_agent_type":"researcher","goal":"查高铁"}`}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// 第二轮:纯文本回复"已经安排研究员去查了"
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
	for _, e := range events {
		if e.Type == AgentEventFinal {
			finalContent = e.Content
		}
	}
	require.NotEmpty(t, finalContent)
	assert.Contains(t, finalContent, "已经安排研究员去查高铁票了")
	assert.Contains(t, finalContent, "任务ID: 43", "调了 delegate,末尾应拼接任务标识")
}

// TestReActAgent_RunStream_NoTaskIDWhenNoDelegate 没调 delegate(直接回复),
// 末尾不应有"任务ID"(幻觉派活时无标识,暴露"说了但没派")。
func TestReActAgent_RunStream_NoTaskIDWhenNoDelegate(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 直接回复(无 tool_call,模拟幻觉派活)
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
			return `{"task_id": 43}`, nil
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
	for _, e := range events {
		if e.Type == AgentEventFinal {
			finalContent = e.Content
		}
	}
	require.NotEmpty(t, finalContent)
	assert.Contains(t, finalContent, "已经安排研究员去查高铁票了")
	assert.NotContains(t, finalContent, "任务ID", "没调 delegate,末尾不应有任务标识(幻觉暴露)")
}
