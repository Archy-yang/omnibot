package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"omnibot/internal/client/llm"
	domainuser "omnibot/internal/domain/user"
	serviceuser "omnibot/internal/service/user"
)

// Local type aliases for testing
type LLMConfigView = serviceuser.LLMConfigView
type FullLLMConfig = serviceuser.FullLLMConfig
type UpdateConfigRequest = serviceuser.UpdateConfigRequest

type mockUserService struct {
	userID       int64
	channelID    string
	created      bool
}

func (m *mockUserService) GetOrCreateByChannel(channelType, channelUserID string) (*domainuser.User, *domainuser.UserChannel, bool, error) {
	m.channelID = channelUserID
	return &domainuser.User{ID: m.userID}, nil, m.created, nil
}

type mockMessageService struct {
	savedUserContent     string
	savedAssistantContent string
}

func (m *mockMessageService) SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error {
	m.savedUserContent = content
	return nil
}

func (m *mockMessageService) SaveAssistantMessage(ctx context.Context, userID int64, content string) error {
	m.savedAssistantContent = content
	return nil
}

func (m *mockMessageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	return []llm.ChatMessage{
		{Role: "user", Content: currentContent},
	}, nil
}

type mockLLMClient struct {
	calledWithMessages []llm.ChatMessage
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	m.calledWithMessages = messages
	return "AI response", nil
}

// LLMConfigService mock
type mockLLMConfigService struct {
	hasConfig    bool
	configView   *LLMConfigView
	fullConfig   *FullLLMConfig
	updateErr    error
	clearErr     error
	lastUpdate   UpdateConfigRequest
}

func (m *mockLLMConfigService) GetConfigView(userID int64) (*LLMConfigView, error) {
	if m.configView != nil {
		return m.configView, nil
	}
	return &LLMConfigView{
		HasConfig:    m.hasConfig,
		APIKeyMasked: "sk-...123",
		BaseURL:      "https://api.openai.com/v1",
		Model:        "gpt-3.5-turbo",
		Provider:     "openai",
		StatusText:   "使用你的自定义模型",
		Temperature:  0.7,
		MaxTokens:    2048,
	}, nil
}

func (m *mockLLMConfigService) UpdateFullConfig(userID int64, req UpdateConfigRequest) error {
	m.lastUpdate = req
	return m.updateErr
}

func (m *mockLLMConfigService) ClearConfig(userID int64) error {
	return m.clearErr
}

func (m *mockLLMConfigService) GetFullConfigForUser(userID int64) (*FullLLMConfig, bool, error) {
	if !m.hasConfig || m.fullConfig == nil {
		return nil, false, nil
	}
	return m.fullConfig, true, nil
}

func TestHandleSendMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

	router := gin.New()
	router.POST("/api/v1/chat/messages", handler.HandleSendMessage)

	// Test request
	reqBody := map[string]string{
		"session_id": "test-session-123",
		"content":    "Hello OmniBot",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AI response")
	assert.Equal(t, "test-session-123", userSvc.channelID)
	assert.Equal(t, "Hello OmniBot", msgSvc.savedUserContent)
	assert.Equal(t, "AI response", msgSvc.savedAssistantContent)
}

func TestHandleGetHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup with mock messages
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

	// Test request
	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=test-session-123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "messages")
}

// ========== LLM 配置接口测试 ==========

func TestHandleGetLLMConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("has custom config", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{
			hasConfig: true,
			configView: &LLMConfigView{
				HasConfig:    true,
				APIKeyMasked: "sk-ab...78",
				BaseURL:      "https://api.openai.com/v1",
				Model:        "gpt-3.5-turbo",
				Provider:     "openai",
				StatusText:   "使用你的自定义模型",
				Temperature:  0.8,
				MaxTokens:    4096,
			},
		}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.GET("/api/v1/user/llm-config", handler.HandleGetLLMConfig)

		req, _ := http.NewRequest("GET", "/api/v1/user/llm-config?session_id=test-session-123", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "has_config")
		assert.Contains(t, w.Body.String(), "openai")
		assert.Contains(t, w.Body.String(), "使用你的自定义模型")
	})

	t.Run("no session id", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: false}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.GET("/api/v1/user/llm-config", handler.HandleGetLLMConfig)

		req, _ := http.NewRequest("GET", "/api/v1/user/llm-config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleUpdateLLMConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("update config success", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: false, updateErr: nil}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id":  "test-session-123",
			"provider":    "qwen",
			"model":       "qwen-turbo",
			"api_key":     "sk-test-key-1234567890abcdefghijk",
			"base_url":    "https://dashscope.aliyuncs.com/compatible-mode/v1",
			"temperature": 0.7,
			"max_tokens":  2048,
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "配置保存成功")
		assert.Equal(t, "qwen", configSvc.lastUpdate.Provider)
		assert.Equal(t, "qwen-turbo", configSvc.lastUpdate.Model)
	})

	t.Run("update with validation error", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{
			hasConfig: false,
			updateErr: &ValidationError{Message: "API Key 长度不正确"},
		}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id": "test-session-123",
			"provider":   "qwen",
			"model":      "qwen-turbo",
			"api_key":    "short",
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("missing required fields", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: false}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"api_key": "sk-test-key",
			// missing session_id, provider, model
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestHandleDeleteLLMConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("delete config success", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: true, clearErr: nil}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.DELETE("/api/v1/user/llm-config", handler.HandleDeleteLLMConfig)

		req, _ := http.NewRequest("DELETE", "/api/v1/user/llm-config?session_id=test-session-123", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "配置已清除")
	})

	t.Run("delete without session id", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: true}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

		router := gin.New()
		router.DELETE("/api/v1/user/llm-config", handler.HandleDeleteLLMConfig)

		req, _ := http.NewRequest("DELETE", "/api/v1/user/llm-config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ========== 聊天注入自定义配置测试 ==========
// 注意：完整的自定义配置测试需要真实的 LLM 服务或更复杂的 mock
// 这里主要测试当没有自定义配置时，系统默认配置正常工作

func TestHandleSendMessage_WithoutCustomLLMConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)

	router := gin.New()
	router.POST("/api/v1/chat/messages", handler.HandleSendMessage)

	reqBody := map[string]string{
		"session_id": "test-session-123",
		"content":    "Hello with default config",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AI response")
	assert.Equal(t, "test-session-123", userSvc.channelID)
}

type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}
