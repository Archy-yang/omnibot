package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	domainuser "omnibot/internal/domain/user"
	agentpkg "omnibot/internal/service/agent"
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
	savedSegments        []conversation.MessageSegment
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

func (m *mockMessageService) SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment) error {
	m.savedAssistantContent = content
	m.savedSegments = segments
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

// ===== Agent stream handler tests =====

// mockAgentService 模拟 AgentService，按预设序列推 AgentEvent 到 channel。
type mockAgentService struct {
	events    []agentpkg.AgentEvent
	runErr    error
	streamErr error
}

func (m *mockAgentService) Run(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	customLLMClient ...agentpkg.LLMClient,
) (*agentpkg.AgentResult, error) {
	if m.runErr != nil {
		return nil, m.runErr
	}
	return &agentpkg.AgentResult{FinalResponse: "noop"}, nil
}

func (m *mockAgentService) RunStream(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	customStreamClient ...agentpkg.StreamingLLMClient,
) (<-chan agentpkg.AgentEvent, error) {
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	ch := make(chan agentpkg.AgentEvent, len(m.events))
	for _, e := range m.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func (m *mockAgentService) DefaultLLMClient() agentpkg.LLMClient { return nil }
func (m *mockAgentService) DefaultStreamingLLMClient() agentpkg.StreamingLLMClient {
	return nil
}

// TestHandleSendMessageAgentStream_TokenAndDone：纯 token 流场景，
// SSE 正文应该包含 event: token + data: {"content":"..."}，最后是 [DONE]。
// 这是「默认全 Agent」体验下最常见的简单提问路径。
func TestHandleSendMessageAgentStream_TokenAndDone(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToken, Content: "你"},
			{Type: agentpkg.AgentEventToken, Content: "好"},
			{Type: agentpkg.AgentEventDone, Content: "你好"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "你好"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))

	out := w.Body.String()
	assert.Contains(t, out, "event: token")
	assert.Contains(t, out, `"content":"你"`)
	assert.Contains(t, out, `"content":"好"`)
	assert.Contains(t, out, "[DONE]")

	// 累计的 token 应该被持久化为 assistant 消息
	assert.Equal(t, "你好", msgSvc.savedAssistantContent)
}

// TestHandleSendMessageAgentStream_ToolCallEvent：工具调用场景，
// SSE 正文应该有 event: tool_call 行携带 tool + label，
// 然后是后续 token 流，最后 [DONE]。
func TestHandleSendMessageAgentStream_ToolCallEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToolCall, ToolName: "get_current_time", ToolLabel: "查询了当前时间"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "get_current_time", ToolResult: "10:30"},
			{Type: agentpkg.AgentEventToken, Content: "现在是 10:30"},
			{Type: agentpkg.AgentEventDone, Content: "现在是 10:30"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "几点了"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	out := w.Body.String()

	// tool_call 事件携带工具名和友好 label
	assert.Contains(t, out, "event: tool_call")
	assert.Contains(t, out, `"tool":"get_current_time"`)
	assert.Contains(t, out, `"label":"查询了当前时间"`)

	// v1.5.3：tool_result 现在向前端暴露（正常结果原样透传），供「展开看详情」
	assert.Contains(t, out, "event: tool_result")
	assert.Contains(t, out, `"result":"10:30"`)

	// token 后跟 [DONE]
	assert.Contains(t, out, "event: token")
	assert.Contains(t, out, "[DONE]")
	assert.Equal(t, "现在是 10:30", msgSvc.savedAssistantContent)

	// tool_result 不计入落库的 assistant 内容
	assert.NotContains(t, msgSvc.savedAssistantContent, "10:30\"")
}

// TestHandleSendMessageAgentStream_ToolResultSanitized：工具执行失败的结果
// 不应原样透传给前端（安全红线：错误不泄露内部实现细节），而是替换为友好文案。
func TestHandleSendMessageAgentStream_ToolResultSanitized(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToolCall, ToolName: "rss_reader", ToolLabel: "读取了 RSS 订阅"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "rss_reader", ToolResult: "工具执行错误: dial tcp 10.0.0.1:443: connection refused"},
			{Type: agentpkg.AgentEventToken, Content: "抱歉，读取失败了。"},
			{Type: agentpkg.AgentEventDone, Content: "抱歉，读取失败了。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "读 rss"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	out := w.Body.String()

	// tool_result 事件存在，但原始错误细节被脱敏
	assert.Contains(t, out, "event: tool_result")
	assert.NotContains(t, out, "connection refused")
	assert.NotContains(t, out, "10.0.0.1")
	assert.NotContains(t, out, "dial tcp")
	assert.Contains(t, out, "工具执行失败")
}

// TestHandleSendMessageAgentStream_EventOrdering：SSE 事件必须保持 LLM 真实时序——
// token → tool_call → tool_result → token，前端据此交错渲染思考过程。
func TestHandleSendMessageAgentStream_EventOrdering(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToken, Content: "让我查一下。"},
			{Type: agentpkg.AgentEventToolCall, ToolName: "get_current_time", ToolLabel: "查询了当前时间"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "get_current_time", ToolResult: "10:30"},
			{Type: agentpkg.AgentEventToken, Content: "现在是 10:30。"},
			{Type: agentpkg.AgentEventDone, Content: "让我查一下。现在是 10:30。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "几点"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	out := w.Body.String()

	// 各事件首次出现的字节位置必须严格递增，证明时序被保留
	idxToken1 := strings.Index(out, "让我查一下")
	idxToolCall := strings.Index(out, "event: tool_call")
	idxToolResult := strings.Index(out, "event: tool_result")
	idxToken2 := strings.Index(out, "现在是 10:30")

	assert.Greater(t, idxToolCall, idxToken1, "tool_call 应在第一段 token 之后")
	assert.Greater(t, idxToolResult, idxToolCall, "tool_result 应在 tool_call 之后")
	assert.Greater(t, idxToken2, idxToolResult, "第二段 token 应在 tool_result 之后")
}

// TestHandleSendMessageAgentStream_PersistsSegments：流式跑完后，应把按时序累积的
// segments（text → tool → text）连同纯文本 content 一起落库，工具结果经脱敏（v1.5.4）。
func TestHandleSendMessageAgentStream_PersistsSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToken, Content: "让我查一下。"},
			{Type: agentpkg.AgentEventToolCall, ToolName: "get_current_time", ToolLabel: "查询了当前时间"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "get_current_time", ToolResult: "10:30"},
			{Type: agentpkg.AgentEventToken, Content: "现在是 10:30。"},
			{Type: agentpkg.AgentEventDone, Content: "让我查一下。现在是 10:30。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "几点"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// content 是纯文本投影
	assert.Equal(t, "让我查一下。现在是 10:30。", msgSvc.savedAssistantContent)

	// segments 按时序：text → tool（含结果）→ text
	require.Len(t, msgSvc.savedSegments, 3)
	assert.Equal(t, "text", msgSvc.savedSegments[0].Type)
	assert.Equal(t, "让我查一下。", msgSvc.savedSegments[0].Content)
	assert.Equal(t, "tool", msgSvc.savedSegments[1].Type)
	assert.Equal(t, "get_current_time", msgSvc.savedSegments[1].Tool)
	assert.Equal(t, "查询了当前时间", msgSvc.savedSegments[1].Label)
	assert.Equal(t, "10:30", msgSvc.savedSegments[1].Result)
	assert.Equal(t, "text", msgSvc.savedSegments[2].Type)
	assert.Equal(t, "现在是 10:30。", msgSvc.savedSegments[2].Content)
}

// TestHandleSendMessageAgentStream_PersistsSanitizedSegments：落库的工具失败结果
// 必须脱敏，不把原始 error 写进 DB（安全红线，v1.5.4）。
func TestHandleSendMessageAgentStream_PersistsSanitizedSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			{Type: agentpkg.AgentEventToolCall, ToolName: "rss_reader", ToolLabel: "读取了 RSS 订阅"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "rss_reader", ToolResult: "工具执行错误: dial tcp 10.0.0.1:443: connection refused"},
			{Type: agentpkg.AgentEventToken, Content: "抱歉，失败了。"},
			{Type: agentpkg.AgentEventDone, Content: "抱歉，失败了。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "读 rss"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Len(t, msgSvc.savedSegments, 2)
	toolSeg := msgSvc.savedSegments[0]
	assert.Equal(t, "tool", toolSeg.Type)
	assert.Equal(t, "工具执行失败", toolSeg.Result)
	assert.NotContains(t, toolSeg.Result, "connection refused")
	assert.NotContains(t, toolSeg.Result, "10.0.0.1")
}

// TestHandleGetHistory_IncludesSegments：历史响应应携带带 segments 的消息的 segments，
// 无 segments 的消息该字段省略（omitempty）（v1.5.4）。
func TestHandleGetHistory_IncludesSegments(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgs := []*conversation.Message{
		conversation.NewUserMessage(42, "几点", ""),
		conversation.NewAssistantMessageWithSegments(42, "现在是 10:30", []conversation.MessageSegment{
			{Type: "tool", Tool: "get_current_time", Label: "查询了当前时间", Result: "10:30"},
			{Type: "text", Content: "现在是 10:30"},
		}),
	}
	msgSvc := &mockMessageService{listMessages: msgs}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, &mockAgentService{})
	router := gin.New()
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?session_id=s&limit=50", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	out := w.Body.String()

	// assistant 消息带 segments
	assert.Contains(t, out, `"segments"`)
	assert.Contains(t, out, `"tool":"get_current_time"`)
	assert.Contains(t, out, `"result":"10:30"`)
	// user 消息无 segments —— 不应每条都带；用户那条不含 segments 字段
	// （粗校验：segments 出现次数应为 1）
	assert.Equal(t, 1, strings.Count(out, `"segments"`))
}

// TestHandleSendMessageAgentStream_StreamOpenError：RunStream 直接返回 error
// （如未配置 streaming client）应该按 SSE error 事件返回，不应让 client 永远等。
func TestHandleSendMessageAgentStream_StreamOpenError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{streamErr: errors.New("streaming client not configured")}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"session_id": "s", "content": "x"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	out := w.Body.String()
	assert.Contains(t, out, "event: error")
	assert.Contains(t, out, "streaming client not configured")
	// 错误路径不应推 [DONE]
	assert.NotContains(t, out, "[DONE]")
}
