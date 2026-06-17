package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockLLMClient 模拟 LLM 客户端，用于测试 Agent 循环
type mockLLMClient struct {
	responses []string
	callCount int
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}) (string, []map[string]interface{}, error) {
	if m.callCount >= len(m.responses) {
		return "fallback response", nil, nil
	}
	resp := m.responses[m.callCount]
	m.callCount++

	if strings.Contains(resp, "tool_calls") {
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
			return "", nil, err
		}
		toolCalls, _ := parsed["tool_calls"].([]interface{})
		result := make([]map[string]interface{}, 0, len(toolCalls))
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				result = append(result, tcMap)
			}
		}
		return "", result, nil
	}
	return resp, nil, nil
}

func TestReActAgent_NoToolCall(t *testing.T) {
	llm := &mockLLMClient{
		responses: []string{"你好！有什么可以帮助你的吗？"},
	}
	registry := NewToolRegistry()

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:    llm,
		ToolRegistry: registry,
		MaxSteps:     10,
		Timeout:      30 * time.Second,
	})

	result, err := agent.Run(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "你好"},
	})
	require.NoError(t, err)
	assert.Equal(t, "你好！有什么可以帮助你的吗？", result.FinalResponse)
	assert.Empty(t, result.Steps)
}

func TestReActAgent_SingleToolCall(t *testing.T) {
	llm := &mockLLMClient{
		responses: []string{
			`{"tool_calls": [{"id": "call_1", "function": {"name": "get_current_time", "arguments": "{}"}}]}`,
			"现在是2026年6月15日上午10点30分",
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "get_current_time",
		Description: "Get current time",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "2026-06-15 10:30:00 CST", nil
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:    llm,
		ToolRegistry: registry,
		MaxSteps:     10,
		Timeout:      30 * time.Second,
	})

	result, err := agent.Run(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "现在几点了？"},
	})
	require.NoError(t, err)
	assert.Equal(t, "现在是2026年6月15日上午10点30分", result.FinalResponse)
	assert.Len(t, result.Steps, 1)
	assert.Equal(t, "get_current_time", result.Steps[0].ToolCall.Name)
	assert.Equal(t, "2026-06-15 10:30:00 CST", result.Steps[0].ToolResult)
}

func TestReActAgent_MaxSteps(t *testing.T) {
	responses := make([]string, 10)
	for i := range responses {
		responses[i] = `{"tool_calls": [{"id": "call_1", "function": {"name": "get_current_time", "arguments": "{}"}}]}`
	}
	llm := &mockLLMClient{responses: responses}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "get_current_time",
		Description: "Get time",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "now", nil
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:    llm,
		ToolRegistry: registry,
		MaxSteps:     5,
		Timeout:      30 * time.Second,
	})

	result, err := agent.Run(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "loop"},
	})
	require.NoError(t, err)
	assert.Equal(t, 5, result.TotalSteps)
	assert.Contains(t, result.FinalResponse, "已达到最大步数")
}

func TestReActAgent_ToolError(t *testing.T) {
	llm := &mockLLMClient{
		responses: []string{
			`{"tool_calls": [{"id": "call_1", "function": {"name": "failing_tool", "arguments": "{}"}}]}`,
			"工具执行失败了，但我可以帮你用其他方式解决...",
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "failing_tool",
		Description: "Always fails",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "", assert.AnError
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:    llm,
		ToolRegistry: registry,
		MaxSteps:     10,
		Timeout:      30 * time.Second,
	})

	result, err := agent.Run(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "test"},
	})
	require.NoError(t, err)
	assert.Len(t, result.Steps, 1)
	assert.NotEmpty(t, result.Steps[0].ToolResult)
}
