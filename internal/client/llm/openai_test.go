package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"wechat-intelligent-bot/pkg/config"
	"wechat-intelligent-bot/pkg/logger"

	"github.com/stretchr/testify/assert"
)

func TestOpenAI_NewProvider(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// arrange
	apiKey := "test-key"
	baseURL := "https://api.openai.com/v1"
	model := "gpt-3.5-turbo"
	timeout := 30 * time.Second

	// act
	provider := NewOpenAIProvider(apiKey, baseURL, model, timeout)

	// assert
	assert.NotNil(t, provider)
	assert.IsType(t, &OpenAIProvider{}, provider)
}

func TestOpenAI_ParseResponse(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// arrange - 使用 httptest server 模拟成功响应
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"id": "chatcmpl-123",
			"object": "chat.completion",
			"created": 1677652288,
			"model": "gpt-3.5-turbo",
			"choices": [{
				"index": 0,
				"message": {
					"role": "assistant",
					"content": "这是大模型生成的回复"
				},
				"finish_reason": "stop"
			}]
		}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL, "gpt-3.5-turbo", 30*time.Second)

	// act
	result, err := provider.ChatCompletion(context.Background(), []ChatMessage{
		{Role: "user", Content: "你好"},
	})

	// assert
	assert.NoError(t, err)
	assert.Equal(t, "这是大模型生成的回复", result)
}

func TestOpenAI_ParseError(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// arrange - 使用 httptest server 模拟错误响应
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{
			"error": {
				"message": "Invalid API key provided",
				"type": "invalid_request_error",
				"code": "invalid_api_key"
			}
		}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL, "gpt-3.5-turbo", 30*time.Second)

	// act
	result, err := provider.ChatCompletion(context.Background(), []ChatMessage{
		{Role: "user", Content: "你好"},
	})

	// assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Invalid API key provided")
	assert.Empty(t, result)
}

func TestOpenAI_HTTPRequest(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// arrange
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// verify request method
		assert.Equal(t, "POST", r.Method)

		// verify path
		assert.Equal(t, "/chat/completions", r.URL.Path)

		// verify headers
		assert.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// verify body
		var body map[string]interface{}
		err := json.NewDecoder(r.Body).Decode(&body)
		assert.NoError(t, err)
		assert.Equal(t, "gpt-3.5-turbo", body["model"])
		assert.NotNil(t, body["messages"])

		// return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "这是模拟的回复"
				}
			}]
		}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL, "gpt-3.5-turbo", 30*time.Second)

	messages := []ChatMessage{
		{Role: "system", Content: "你是一个助手"},
		{Role: "user", Content: "你好"},
	}

	// act
	result, err := provider.ChatCompletion(context.Background(), messages)

	// assert
	assert.NoError(t, err)
	assert.Equal(t, "这是模拟的回复", result)
}

func TestOpenAI_WithBaseURL(t *testing.T) {
	// 初始化日志
	logger.Init(config.LoggerConfig{
		Level: "info",
	})

	// arrange - custom endpoint test
	receivedPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"choices": [{"message": {"content": "自定义端点回复"}}]}`))
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-key", server.URL, "custom-model", 10*time.Second)

	// act
	result, err := provider.ChatCompletion(context.Background(), []ChatMessage{
		{Role: "user", Content: "测试"},
	})

	// assert
	assert.NoError(t, err)
	assert.Equal(t, "自定义端点回复", result)
	assert.Equal(t, "/chat/completions", receivedPath)
}
