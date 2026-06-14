package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLLMConfig_IsEnabled(t *testing.T) {
	tests := []struct {
		name     string
		apiKey   string
		status   int8
		expected bool
	}{
		{"正常配置", "sk-123", 0, true},
		{"空 Key", "", 0, false},
		{"禁用状态", "sk-123", 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LLMConfig{APIKey: tt.apiKey, Status: tt.status}
			assert.Equal(t, tt.expected, cfg.IsEnabled())
		})
	}
}

func TestLLMConfig_GetBaseURL(t *testing.T) {
	customURL := "https://custom.api.com/v1"
	empty := ""

	tests := []struct {
		name     string
		baseURL  *string
		expected string
	}{
		{"自定义地址", &customURL, customURL},
		{"nil 使用默认", nil, "https://api.openai.com/v1"},
		{"空字符串使用默认", &empty, "https://api.openai.com/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LLMConfig{BaseURL: tt.baseURL}
			assert.Equal(t, tt.expected, cfg.GetBaseURL())
		})
	}
}

func TestLLMConfig_GetModel(t *testing.T) {
	custom := "gpt-4"
	empty := ""

	tests := []struct {
		name     string
		model    *string
		expected string
	}{
		{"自定义模型", &custom, custom},
		{"nil 使用默认", nil, "gpt-4o-mini"},
		{"空字符串使用默认", &empty, "gpt-4o-mini"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &LLMConfig{Model: tt.model}
			assert.Equal(t, tt.expected, cfg.GetModel())
		})
	}
}
