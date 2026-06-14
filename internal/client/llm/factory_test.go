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

func (m *mockProvider) StreamChatCompletion(ctx context.Context, messages []ChatMessage) (<-chan StreamChunk, error) {
	return nil, ErrStreamingNotSupported
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

func TestNewClientFromUserConfig_Routing(t *testing.T) {
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	tests := []struct {
		name         string
		provider     string
		expectedType interface{}
	}{
		{"openai routes to OpenAIProvider", "openai", &OpenAIProvider{}},
		{"baidu_qianfan routes to OpenAIProvider", "baidu_qianfan", &OpenAIProvider{}},
		{"volcengine routes to OpenAIProvider", "volcengine", &OpenAIProvider{}},
		{"aliyun_qwen routes to OpenAIProvider", "aliyun_qwen", &OpenAIProvider{}},
		{"custom_openai_compatible routes to OpenAIProvider", "custom_openai_compatible", &OpenAIProvider{}},
		{"qwen routes to QwenProvider", "qwen", &QwenProvider{}},
		{"doubao routes to DoubaoProvider", "doubao", &DoubaoProvider{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := UserConfig{
				Provider: tt.provider,
				APIKey:   "test-key",
				BaseURL:  "https://example.com/v1",
				Model:    "test-model",
			}
			client, err := NewClientFromUserConfig(cfg)
			assert.NoError(t, err)
			assert.NotNil(t, client)
			assert.IsType(t, tt.expectedType, client.defaultProvider)
		})
	}
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
