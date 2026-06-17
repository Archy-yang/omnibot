package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
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
	listMessages         []*conversation.Message
	listErr              error
	listCalledLimit      int
	listCalledBefore     int64
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

func (m *mockMessageService) ListByUser(ctx context.Context, userID int64, limit int, before int64) ([]*conversation.Message, error) {
	m.listCalledLimit = limit
	m.listCalledBefore = before
	return m.listMessages, m.listErr
}

type mockLLMClient struct {
	calledWithMessages []llm.ChatMessage
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error) {
	m.calledWithMessages = messages
	return "AI response", nil
}

func (m *mockLLMClient) StreamChatCompletion(ctx context.Context, messages []llm.ChatMessage) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk, 10)
	go func() {
		defer close(ch)
		for _, r := range []string{"你", "好", "，", "世", "界"} {
			ch <- llm.StreamChunk{Content: r}
		}
		ch <- llm.StreamChunk{Done: true}
	}()
	return ch, nil
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

func (m *mockLLMConfigService) ListProviderOptions() []serviceuser.ProviderOption {
	return []serviceuser.ProviderOption{
		{
			Value:          "openai",
			Label:          "OpenAI 官方",
			Mode:           "openai_compatible",
			Status:         "available",
			DefaultBaseURL: "https://api.openai.com/v1",
			DefaultModel:   "gpt-4o-mini",
			Description:    "OpenAI 官方 Chat Completions API。",
		},
		{
			Value:          "qianfan_native",
			Label:          "百度千帆专用",
			Mode:           "native",
			Status:         "disabled",
			Description:    "百度千帆原生接口。",
			DisabledReason: "专用接口暂不可用，请使用 OpenAI 兼容模式。",
		},
	}
}

func TestHandleSendMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Setup
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

	// Test request
	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=test-session-123", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "messages")
}

// TestHandleGetHistory_ReturnsStoredMessages 回归测试：HandleGetHistory 必须真正
// 通过 MessageService.ListByUser 把库里的消息读出来，按时间正序返回；多取一条用于
// 判断 has_more 翻页边界。这是为了防止再次出现「Handler 直接返回空数组」的桩实现。
func TestHandleGetHistory_ReturnsStoredMessages(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now()
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{
		listMessages: []*conversation.Message{
			{ID: 10, UserID: 42, Role: conversation.RoleUser, Content: "你好", CreatedAt: now.Add(-2 * time.Minute)},
			{ID: 11, UserID: 42, Role: conversation.RoleAssistant, Content: "你好，有什么可以帮你？", CreatedAt: now.Add(-1 * time.Minute)},
		},
	}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)
	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=test-session-123&limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// fetchLimit 应为 limit + 1，用来判断是否还有更多历史
	assert.Equal(t, 51, msgSvc.listCalledLimit)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Messages []MessageDTO `json:"messages"`
			HasMore  bool         `json:"has_more"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	assert.False(t, resp.Data.HasMore)
	require.Len(t, resp.Data.Messages, 2)
	assert.Equal(t, int64(10), resp.Data.Messages[0].ID)
	assert.Equal(t, "你好", resp.Data.Messages[0].Content)
	assert.Equal(t, "user", resp.Data.Messages[0].Role)
	assert.Equal(t, int64(11), resp.Data.Messages[1].ID)
	assert.Equal(t, "assistant", resp.Data.Messages[1].Role)
}

// TestHandleGetHistory_HasMore 验证当 repo 返回的条数超过 limit 时，
// 多取的那条会被丢弃，且 has_more=true 通知前端可以继续翻页。
func TestHandleGetHistory_HasMore(t *testing.T) {
	gin.SetMode(gin.TestMode)

	now := time.Now()
	// 构造 3 条消息但请求 limit=2，模拟「还有更早的历史」场景
	msgs := []*conversation.Message{
		{ID: 100, UserID: 42, Role: conversation.RoleUser, Content: "最旧", CreatedAt: now.Add(-3 * time.Minute)},
		{ID: 101, UserID: 42, Role: conversation.RoleAssistant, Content: "中间", CreatedAt: now.Add(-2 * time.Minute)},
		{ID: 102, UserID: 42, Role: conversation.RoleUser, Content: "最新", CreatedAt: now.Add(-1 * time.Minute)},
	}
	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{listMessages: msgs}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, nil)

	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)
	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=s&limit=2", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data struct {
			Messages []MessageDTO `json:"messages"`
			HasMore  bool         `json:"has_more"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Data.HasMore)
	require.Len(t, resp.Data.Messages, 2)
	// 多取的「最旧」(ID=100) 应该被去掉，只返回最近 2 条
	assert.Equal(t, int64(101), resp.Data.Messages[0].ID)
	assert.Equal(t, int64(102), resp.Data.Messages[1].ID)
}

// TestHandleGetHistory_NewUserReturnsEmpty 新用户没有历史，直接返回空数组。
func TestHandleGetHistory_NewUserReturnsEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)
	userSvc := &mockUserService{userID: 42, created: true} // 新建用户
	msgSvc := &mockMessageService{}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, nil)

	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)
	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=brand-new", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// 新用户路径不应触碰消息服务
	assert.Equal(t, 0, msgSvc.listCalledLimit)
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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

	t.Run("update openai compatible preset success", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: false, updateErr: nil}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id":  "test-session-123",
			"provider":    "aliyun_qwen",
			"model":       "qwen-plus",
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
		assert.Equal(t, "aliyun_qwen", configSvc.lastUpdate.Provider)
		assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", configSvc.lastUpdate.BaseURL)
		assert.Equal(t, "qwen-plus", configSvc.lastUpdate.Model)
	})

	t.Run("native provider disabled error", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{
			hasConfig: false,
			updateErr: errors.New("专用接口暂不可用，请使用 OpenAI 兼容模式。"),
		}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

		router := gin.New()
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"session_id":  "test-session-123",
			"provider":    "qwen_native",
			"model":       "qwen-plus",
			"api_key":     "sk-test-key-1234567890abcdefghijk",
			"base_url":    "https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation",
			"temperature": 0.7,
			"max_tokens":  2048,
		}
		body, _ := json.Marshal(reqBody)

		req, _ := http.NewRequest("PUT", "/api/v1/user/llm-config", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
		assert.Contains(t, w.Body.String(), "专用接口暂不可用")
	})
}

func TestHandleDeleteLLMConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("delete config success", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{hasConfig: true, clearErr: nil}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

		router := gin.New()
		router.DELETE("/api/v1/user/llm-config", handler.HandleDeleteLLMConfig)

		req, _ := http.NewRequest("DELETE", "/api/v1/user/llm-config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

// ========== LLM 提供商列表接口测试 ==========

func TestHandleGetLLMProviders(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("returns provider options with success/data wrapping", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

		router := gin.New()
		router.GET("/api/v1/user/llm-providers", handler.HandleGetLLMProviders)

		req, _ := http.NewRequest("GET", "/api/v1/user/llm-providers", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				Providers []map[string]interface{} `json:"providers"`
			} `json:"data"`
		}
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		assert.NoError(t, err)
		assert.True(t, resp.Success)
		assert.Len(t, resp.Data.Providers, 2)

		// Check available provider fields
		available := resp.Data.Providers[0]
		assert.Equal(t, "openai", available["value"])
		assert.Equal(t, "OpenAI 官方", available["label"])
		assert.Equal(t, "openai_compatible", available["mode"])
		assert.Equal(t, "available", available["status"])
		assert.Equal(t, "https://api.openai.com/v1", available["default_base_url"])
		assert.Equal(t, "gpt-4o-mini", available["default_model"])
		assert.Contains(t, available, "description")

		// Check disabled provider fields
		disabled := resp.Data.Providers[1]
		assert.Equal(t, "qianfan_native", disabled["value"])
		assert.Equal(t, "disabled", disabled["status"])
		assert.NotEmpty(t, disabled["disabled_reason"])
	})

	t.Run("no session id required", func(t *testing.T) {
		userSvc := &mockUserService{userID: 42, created: false}
		msgSvc := &mockMessageService{}
		llmClient := &mockLLMClient{}
		configSvc := &mockLLMConfigService{}

		handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

		router := gin.New()
		router.GET("/api/v1/user/llm-providers", handler.HandleGetLLMProviders)

		// No session_id needed - provider list is public
		req, _ := http.NewRequest("GET", "/api/v1/user/llm-providers", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
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

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

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

// ========== SSE 流式响应测试 ==========

func TestHandleSendMessageStream_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

	router := gin.New()
	router.POST("/api/v1/chat/messages/stream", handler.HandleSendMessageStream)

	reqBody := map[string]string{
		"session_id": "test-session-123",
		"content":    "你好",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Body.String(), "data: ")
	assert.Contains(t, w.Body.String(), `"content":"你"`)
	assert.Contains(t, w.Body.String(), `"content":"好"`)
	assert.Contains(t, w.Body.String(), "[DONE]")
}

func TestHandleSendMessageStream_InvalidBody(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	llmClient := &mockLLMClient{}
	configSvc := &mockLLMConfigService{hasConfig: false}

	handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{}, nil)

	router := gin.New()
	router.POST("/api/v1/chat/messages/stream", handler.HandleSendMessageStream)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/stream", bytes.NewBuffer([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
