package llm

import (
	"context"
	"errors"
	"testing"

	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"github.com/stretchr/testify/assert"
)

type mockProvider struct {
	shouldFail bool
	response   string
}

func (m *mockProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error) {
	if m.shouldFail {
		return "", errors.New("mock failure")
	}
	return m.response, nil
}

func TestFactory_CreateClient_Success(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"qwen": {
				APIKey:  "test-key-qwen",
				Model:   "qwen-max",
				Timeout: "30s",
			},
			"doubao": {
				APIKey:  "test-key-doubao",
				Model:   "doubao-pro",
				Timeout: "30s",
			},
		},
		Routing: config.LMRoutingConfig{
			Default:       "qwen",
			FallbackOrder: []string{"qwen", "doubao"},
		},
	}

	// 执行
	client, err := NewClient(cfg)

	// 断言
	assert.NoError(t, err)
	assert.NotNil(t, client)
}

func TestFactory_CreateClient_NotFoundDefault(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	cfg := config.LLMConfig{
		Providers: map[string]config.ProviderConfig{
			"qwen": {
				APIKey:  "test-key-qwen",
				Model:   "qwen-max",
				Timeout: "30s",
			},
		},
		Routing: config.LMRoutingConfig{
			Default:       "doubao",
			FallbackOrder: []string{"doubao"},
		},
	}

	// 执行
	client, err := NewClient(cfg)

	// 断言
	assert.Error(t, err)
	assert.Nil(t, client)
	assert.Contains(t, err.Error(), "default provider")
	assert.Contains(t, err.Error(), "not found")
}

func TestClient_ChatCompletion_Fallback(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	client := &Client{
		defaultProvider: &mockProvider{shouldFail: true},
		fallbackProviders: []LLMProvider{
			&mockProvider{shouldFail: false, response: "fallback response"},
		},
	}

	// 执行
	ctx := context.Background()
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	resp, err := client.ChatCompletion(ctx, messages)

	// 断言
	assert.NoError(t, err)
	assert.Equal(t, "fallback response", resp)
}

func TestClient_ChatCompletion_AllFailed(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// 安排
	client := &Client{
		defaultProvider: &mockProvider{shouldFail: true},
		fallbackProviders: []LLMProvider{
			&mockProvider{shouldFail: true},
		},
	}

	// 执行
	ctx := context.Background()
	messages := []ChatMessage{
		{Role: "user", Content: "hello"},
	}
	resp, err := client.ChatCompletion(ctx, messages)

	// 断言
	assert.Error(t, err)
	assert.Empty(t, resp)
	assert.Contains(t, err.Error(), "all providers failed")
}
