package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolRegistry_Register(t *testing.T) {
	registry := NewToolRegistry()
	tool := Tool{
		Name:        "test_tool",
		Description: "A test tool",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"arg1": map[string]interface{}{
					"type":        "string",
					"description": "test argument",
				},
			},
			"required": []string{"arg1"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "ok", nil
		},
	}

	err := registry.Register(tool)
	assert.NoError(t, err)

	// 重名注册应该失败
	err = registry.Register(tool)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestToolRegistry_Get(t *testing.T) {
	registry := NewToolRegistry()
	tool := Tool{
		Name:        "get_time",
		Description: "Get current time",
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "now", nil
		},
	}
	_ = registry.Register(tool)

	found, ok := registry.Get("get_time")
	assert.True(t, ok)
	assert.Equal(t, "get_time", found.Name)

	_, ok = registry.Get("nonexistent")
	assert.False(t, ok)
}

func TestToolRegistry_ListAll(t *testing.T) {
	registry := NewToolRegistry()
	_ = registry.Register(Tool{
		Name: "a", Description: "A",
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) { return "", nil },
	})
	_ = registry.Register(Tool{
		Name: "b", Description: "B",
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) { return "", nil },
	})

	all := registry.ListAll()
	assert.Len(t, all, 2)
}

func TestToolRegistry_ToOpenAITools(t *testing.T) {
	registry := NewToolRegistry()
	_ = registry.Register(Tool{
		Name:        "get_time",
		Description: "Get the current time",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) { return "", nil },
	})

	oaiTools := registry.ToOpenAITools()
	require.Len(t, oaiTools, 1)
	assert.Equal(t, "function", oaiTools[0]["type"])

	fn, ok := oaiTools[0]["function"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "get_time", fn["name"])
	assert.Equal(t, "Get the current time", fn["description"])
}
