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

// injectUserID 是测试用的 middleware,替代真实 JWT 鉴权:
// 直接把指定 user_id 塞进 gin.Context,让 handler 通过
// c.GetInt64(middleware.AuthUserIDKey) 拿到测试身份。
func injectUserID(uid int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("user_id", uid)
		c.Next()
	}
}

type mockUserService struct {
	userID  int64
	created bool
}

func (m *mockUserService) GetOrCreateByChannel(channelType, channelUserID string) (*domainuser.User, *domainuser.UserChannel, bool, error) {
	return &domainuser.User{ID: m.userID}, nil, m.created, nil
}

type mockMessageService struct {
	savedUserContent      string
	savedAssistantContent string
	savedSegments         []conversation.MessageSegment
	savedSteps            []*conversation.AgentStep
	// ctxMessages 非空时 BuildContextMessages 返回它(模拟真实历史,报告锚定测试用)
	ctxMessages         []llm.ChatMessage
	listMessages        []*conversation.Message
	listErr             error
	listCalledLimit     int
	listCalledBefore    int64
	reportSaved         bool
	savedReportTaskID   int64
	savedReportContent  string
	savedReportSegments []conversation.MessageSegment
	savedReportSteps    []*conversation.AgentStep
}

func (m *mockMessageService) SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error {
	m.savedUserContent = content
	return nil
}

func (m *mockMessageService) SaveAssistantMessage(ctx context.Context, userID int64, content string) error {
	m.savedAssistantContent = content
	return nil
}

func (m *mockMessageService) SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error {
	m.savedAssistantContent = content
	m.savedSegments = segments
	m.savedSteps = steps
	return nil
}

func (m *mockMessageService) SaveAssistantMessageWithToolCalls(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, toolCalls *string, steps []*conversation.AgentStep) error {
	m.savedAssistantContent = content
	m.savedSegments = segments
	m.savedSteps = steps
	return nil
}

func (m *mockMessageService) SaveReportMessage(ctx context.Context, userID, taskID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error {
	m.reportSaved = true
	m.savedReportTaskID = taskID
	m.savedReportContent = content
	m.savedReportSegments = segments
	m.savedReportSteps = steps
	return nil
}

func (m *mockMessageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	// ctxMessages 非空时作为历史,末尾追加当前消息(贴近真实实现;报告锚定等测试用)
	if m.ctxMessages != nil {
		return append(append([]llm.ChatMessage{}, m.ctxMessages...),
			llm.ChatMessage{Role: "user", Content: currentContent}), nil
	}
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
	hasConfig  bool
	configView *LLMConfigView
	fullConfig *FullLLMConfig
	updateErr  error
	clearErr   error
	lastUpdate UpdateConfigRequest
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages", handler.HandleSendMessage)

	// Test request
	reqBody := map[string]string{
		"content": "Hello OmniBot",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AI response")
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
	router.Use(injectUserID(42))
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages", nil)
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
	router.Use(injectUserID(42))
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?limit=50", nil)
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
	router.Use(injectUserID(42))
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)
	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?limit=2", nil)
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
		router.Use(injectUserID(42))
		router.GET("/api/v1/user/llm-config", handler.HandleGetLLMConfig)

		req, _ := http.NewRequest("GET", "/api/v1/user/llm-config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "has_config")
		assert.Contains(t, w.Body.String(), "openai")
		assert.Contains(t, w.Body.String(), "使用你的自定义模型")
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
		router.Use(injectUserID(42))
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
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
		router.Use(injectUserID(42))
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"provider": "qwen",
			"model":    "qwen-turbo",
			"api_key":  "short",
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
		router.Use(injectUserID(42))
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
			"api_key": "sk-test-key",
			// missing provider, model
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
		router.Use(injectUserID(42))
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
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
		router.Use(injectUserID(42))
		router.PUT("/api/v1/user/llm-config", handler.HandleUpdateLLMConfig)

		reqBody := map[string]interface{}{
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
		router.Use(injectUserID(42))
		router.DELETE("/api/v1/user/llm-config", handler.HandleDeleteLLMConfig)

		req, _ := http.NewRequest("DELETE", "/api/v1/user/llm-config", nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), "配置已清除")
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages", handler.HandleSendMessage)

	reqBody := map[string]string{
		"content": "Hello with default config",
	}
	body, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "AI response")
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/stream", handler.HandleSendMessageStream)

	reqBody := map[string]string{
		"content": "你好",
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/stream", handler.HandleSendMessageStream)

	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/stream", bytes.NewBuffer([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// ===== Agent stream handler tests =====

// mockAgentService 模拟 AgentService，按预设序列推 AgentEvent 到 channel。
// v1.6: runResult 用于让 Run 返回预设 AgentResult(测同步端点落 Records 时用)。
type mockAgentService struct {
	events    []agentpkg.AgentEvent
	runErr    error
	streamErr error
	runResult *agentpkg.AgentResult
	// 捕获 RunStream 收到的 conversation(前置汇报测试用)
	capturedStreamConversation []map[string]interface{}
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
	if m.runResult != nil {
		return m.runResult, nil
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
	m.capturedStreamConversation = conversation
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "你好"})
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "几点了"})
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "读 rss"})
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "几点"})
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
			{Type: agentpkg.AgentEventLLMCall, LLMRequest: `[{"role":"user"}]`, LLMResponse: `{"tool_calls":[...]}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 300},
			{Type: agentpkg.AgentEventThought, Content: "让我查一下。"},
			{Type: agentpkg.AgentEventToolCall, ToolName: "get_current_time", ToolLabel: "查询了当前时间"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "get_current_time", ToolResult: "10:30", ToolArguments: "{}", StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 5},
			{Type: agentpkg.AgentEventToken, Content: "现在是 10:30。"},
			{Type: agentpkg.AgentEventLLMCall, LLMRequest: `[...]`, LLMResponse: `{"content":"现在是 10:30。"}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 250},
			{Type: agentpkg.AgentEventFinal, Content: "现在是 10:30。"},
			{Type: agentpkg.AgentEventDone, Content: "让我查一下。现在是 10:30。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "几点"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// content 是最终回复的纯文本投影(思考模式改造:只取最后一个 text 段,
	// 不含前一段思考文本"让我查一下。",避免污染下一轮上下文)
	assert.Equal(t, "现在是 10:30。", msgSvc.savedAssistantContent)

	// segments 按时序：text → tool（含结果）→ text
	require.Len(t, msgSvc.savedSegments, 3)
	assert.Equal(t, "text", msgSvc.savedSegments[0].Type)
	assert.Equal(t, "thought", msgSvc.savedSegments[0].Role, "思考轮 text 段标 thought")
	assert.Equal(t, "让我查一下。", msgSvc.savedSegments[0].Content)
	assert.Equal(t, "tool", msgSvc.savedSegments[1].Type)
	assert.Equal(t, "get_current_time", msgSvc.savedSegments[1].Tool)
	assert.Equal(t, "查询了当前时间", msgSvc.savedSegments[1].Label)
	assert.Equal(t, "10:30", msgSvc.savedSegments[1].Result)
	assert.Equal(t, "text", msgSvc.savedSegments[2].Type)
	assert.Equal(t, "final", msgSvc.savedSegments[2].Role, "回复轮 text 段标 final")
	assert.Equal(t, "现在是 10:30。", msgSvc.savedSegments[2].Content)

	// v1.5.5：agent_steps 步骤链按 seq 有序：llm_call → tool_call → llm_call
	require.Len(t, msgSvc.savedSteps, 3)
	assert.Equal(t, conversation.StepKindLLMCall, msgSvc.savedSteps[0].Kind)
	assert.Equal(t, 0, msgSvc.savedSteps[0].Seq)
	assert.Equal(t, conversation.StepKindToolCall, msgSvc.savedSteps[1].Kind)
	assert.Equal(t, 1, msgSvc.savedSteps[1].Seq)
	assert.Equal(t, "get_current_time", msgSvc.savedSteps[1].Tool)
	assert.Equal(t, "10:30", msgSvc.savedSteps[1].Response) // 工具步骤 response 是原始结果
	assert.Equal(t, conversation.StepKindLLMCall, msgSvc.savedSteps[2].Kind)
	assert.Equal(t, 2, msgSvc.savedSteps[2].Seq)
}

// TestHandleSendMessageAgentStream_ThoughtVsFinalSplit：思考模式展示改造--
// 多轮 ReAct 中,中间轮的思考文本("我来读取…""让我尝试…")是过程,只有最后一轮无工具的
// text 段才是最终回复。落库的 content 必须只含最终回复(供下一轮上下文 + 主气泡展示),
// 不能把思考文本拼进去污染上下文。segments 仍完整保留(思考+最终)供思考块回看。
//
// 事件序列模拟用户报告的 aihot 网站资讯场景:
//
//	text(思考1) -> tool -> text(思考2) -> tool -> text(最终回复)
func TestHandleSendMessageAgentStream_ThoughtVsFinalSplit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 42, created: false}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		events: []agentpkg.AgentEvent{
			// 第1轮:思考文本 + 调 rss_reader 探测
			{Type: agentpkg.AgentEventToken, Content: "好的,我来读取这个网站的最新文章资讯!"},
			{Type: agentpkg.AgentEventLLMCall, LLMResponse: `{"tool_calls":[...]}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 400},
			{Type: agentpkg.AgentEventThought, Content: "好的,我来读取这个网站的最新文章资讯!"},
			{Type: agentpkg.AgentEventToolCall, ToolName: "rss_reader", ToolLabel: "读取了 RSS 订阅"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "rss_reader", ToolResult: "普通网站首页,非 RSS", ToolArguments: `{"url":"https://aihot.virxact.com/"}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 800},
			// 第2轮:继续思考 + 再调 rss_reader 试常见 RSS 路径
			{Type: agentpkg.AgentEventToken, Content: "让我尝试几个常见的 RSS 订阅地址看看能否找到。"},
			{Type: agentpkg.AgentEventLLMCall, LLMResponse: `{"tool_calls":[...]}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 350},
			{Type: agentpkg.AgentEventThought, Content: "让我尝试几个常见的 RSS 订阅地址看看能否找到。"},
			{Type: agentpkg.AgentEventToolCall, ToolName: "rss_reader", ToolLabel: "读取了 RSS 订阅"},
			{Type: agentpkg.AgentEventToolResult, ToolName: "rss_reader", ToolResult: "找到 AI HOT 的 RSS 源", ToolArguments: `{"url":"https://aihot.virxact.com/feed"}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 600},
			// 第3轮:最终回复(无工具,管家口吻)
			{Type: agentpkg.AgentEventToken, Content: "我帮你查了 AI HOT 的最新资讯,以下是三篇文章…"},
			{Type: agentpkg.AgentEventLLMCall, LLMResponse: `{"content":"我帮你查了…"}`, StepStatus: agentpkg.StepStatusSuccess, StepDurationMs: 500},
			{Type: agentpkg.AgentEventFinal, Content: "我帮你查了 AI HOT 的最新资讯,以下是三篇文章…"},
			{Type: agentpkg.AgentEventDone, Content: ""},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "获取一下最新的文章资讯"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// 核心断言:content 只含最后一个 text 段(最终回复),不含两段思考文本。
	// 这保证 BuildContextMessages 取 msg.Content 时,下一轮上下文不被思考文本污染。
	finalReply := "我帮你查了 AI HOT 的最新资讯,以下是三篇文章…"
	assert.Equal(t, finalReply, msgSvc.savedAssistantContent)
	assert.NotContains(t, msgSvc.savedAssistantContent, "我来读取这个网站")
	assert.NotContains(t, msgSvc.savedAssistantContent, "让我尝试几个常见的 RSS")

	// segments 完整保留:思考1 -> tool -> 思考2 -> tool -> 最终回复(5 段)
	require.Len(t, msgSvc.savedSegments, 5)
	assert.Equal(t, "text", msgSvc.savedSegments[0].Type)
	assert.Equal(t, "thought", msgSvc.savedSegments[0].Role)
	assert.Equal(t, "好的,我来读取这个网站的最新文章资讯!", msgSvc.savedSegments[0].Content)
	assert.Equal(t, "tool", msgSvc.savedSegments[1].Type)
	assert.Equal(t, "rss_reader", msgSvc.savedSegments[1].Tool)
	assert.Equal(t, "text", msgSvc.savedSegments[2].Type)
	assert.Equal(t, "thought", msgSvc.savedSegments[2].Role)
	assert.Equal(t, "让我尝试几个常见的 RSS 订阅地址看看能否找到。", msgSvc.savedSegments[2].Content)
	assert.Equal(t, "tool", msgSvc.savedSegments[3].Type)
	assert.Equal(t, "text", msgSvc.savedSegments[4].Type)
	assert.Equal(t, "final", msgSvc.savedSegments[4].Role, "最终回复段标 final")
	assert.Equal(t, finalReply, msgSvc.savedSegments[4].Content)
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
			{Type: agentpkg.AgentEventToolResult, ToolName: "rss_reader",
				ToolResult:     "工具执行错误: dial tcp 10.0.0.1:443: connection refused",
				ToolArguments:  `{"url":"https://x.com/rss"}`,
				StepStatus:     agentpkg.StepStatusError,
				StepDurationMs: 1200},
			{Type: agentpkg.AgentEventToken, Content: "抱歉，失败了。"},
			{Type: agentpkg.AgentEventDone, Content: "抱歉，失败了。"},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "读 rss"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// 展示层：segment.result 脱敏
	require.Len(t, msgSvc.savedSegments, 2)
	toolSeg := msgSvc.savedSegments[0]
	assert.Equal(t, "tool", toolSeg.Type)
	assert.Equal(t, "工具执行失败", toolSeg.Result)
	assert.NotContains(t, toolSeg.Result, "connection refused")
	assert.NotContains(t, toolSeg.Result, "10.0.0.1")

	// 记录层：agent_steps 保留完整原始结果（含真实错误）+ status + duration（v1.5.5）
	require.Len(t, msgSvc.savedSteps, 1)
	step := msgSvc.savedSteps[0]
	assert.Equal(t, "rss_reader", step.Tool)
	assert.Equal(t, `{"url":"https://x.com/rss"}`, step.Request)
	assert.Contains(t, step.Response, "connection refused") // 原始错误未脱敏
	assert.Equal(t, conversation.StepStatusError, step.Status)
	assert.Equal(t, int64(1200), step.DurationMs)
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
	router.Use(injectUserID(42))
	router.GET("/api/v1/chat/messages", handler.HandleGetHistory)

	req, _ := http.NewRequest("GET", "/api/v1/chat/messages?limit=50", nil)
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
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "x"})
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

// TestHandleSendMessageAgent_PersistsRecordsAsAgentSteps (v1.6):
// 同步端点 /messages/agent 必须把 AgentResult.Records 转成 conversation.AgentStep
// 落库,与流式端点行为对齐(同步/流式两条返回形式,记录单一来源)。
func TestHandleSendMessageAgent_PersistsRecordsAsAgentSteps(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 7}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		runResult: &agentpkg.AgentResult{
			FinalResponse: "现在是 10:30",
			Records: []agentpkg.StepRecord{
				{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, DurationMs: 100, Request: "[req1]", Response: `{"tool_calls":[...]}`},
				{Kind: agentpkg.StepKindToolCall, Status: agentpkg.StepStatusSuccess, DurationMs: 5, Tool: "get_current_time", Request: "{}", Response: "10:30"},
				{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, DurationMs: 200, Request: "[req2]", Response: `{"content":"现在是 10:30"}`},
			},
		},
	}
	llmCfgSvc := &mockLLMConfigService{
		hasConfig: true,
		fullConfig: &FullLLMConfig{
			Provider: "test", APIKey: "k", BaseURL: "http://x", Model: "test-model-x",
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, llmCfgSvc, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent", handler.HandleSendMessageAgent)

	body, _ := json.Marshal(map[string]string{"content": "几点了"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "现在是 10:30", msgSvc.savedAssistantContent)
	assert.Nil(t, msgSvc.savedSegments)
	require.Len(t, msgSvc.savedSteps, 3)
	assert.Equal(t, conversation.StepKindLLMCall, msgSvc.savedSteps[0].Kind)
	assert.Equal(t, 0, msgSvc.savedSteps[0].Seq)
	assert.Equal(t, "test-model-x", msgSvc.savedSteps[0].Model)
	assert.Equal(t, conversation.StepKindToolCall, msgSvc.savedSteps[1].Kind)
	assert.Equal(t, 1, msgSvc.savedSteps[1].Seq)
	assert.Equal(t, "get_current_time", msgSvc.savedSteps[1].Tool)
	assert.Equal(t, "10:30", msgSvc.savedSteps[1].Response)
	assert.Equal(t, conversation.StepKindLLMCall, msgSvc.savedSteps[2].Kind)
	assert.Equal(t, 2, msgSvc.savedSteps[2].Seq)
	assert.Equal(t, "test-model-x", msgSvc.savedSteps[2].Model)
}

// TestHandleSendMessageAgent_NoCustomConfig_ModelEmpty (v1.6):
// 无用户自定义配置时,llm_call step 的 Model 字段留空(handler 层填充,agent 包拿不到模型名)。
func TestHandleSendMessageAgent_NoCustomConfig_ModelEmpty(t *testing.T) {
	gin.SetMode(gin.TestMode)

	userSvc := &mockUserService{userID: 7}
	msgSvc := &mockMessageService{}
	agentSvc := &mockAgentService{
		runResult: &agentpkg.AgentResult{
			FinalResponse: "你好",
			Records: []agentpkg.StepRecord{
				{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, DurationMs: 50, Request: "[req]", Response: `{"content":"你好"}`},
			},
		},
	}
	handler := NewHandler(userSvc, msgSvc, &mockLLMClient{}, &mockLLMConfigService{hasConfig: false}, &mockMemoryService{}, agentSvc)
	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent", handler.HandleSendMessageAgent)

	body, _ := json.Marshal(map[string]string{"content": "你好"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, msgSvc.savedSteps, 1)
	assert.Equal(t, "", msgSvc.savedSteps[0].Model)
}
