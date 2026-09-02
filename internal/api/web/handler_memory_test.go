package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryService struct {
	memories        []*memorydomain.Memory
	rememberErr     error
	listErr         error
	clearErr        error
	clearedSource   string
	deleteErr       error
	updateErr       error
	rememberUserID  int64
	rememberContent string
	listUserID      int64
	clearUserID     int64
	clearCalled     bool
}

func (m *mockMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	m.rememberUserID = userID
	m.rememberContent = content
	if m.rememberErr != nil {
		return nil, m.rememberErr
	}
	memory := &memorydomain.Memory{
		ID:        99,
		UserID:    userID,
		Content:   strings.TrimSpace(content),
		CreatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
	}
	m.memories = append(m.memories, memory)
	return memory, nil
}

func (m *mockMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	m.listUserID = userID
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.memories, nil
}

func (m *mockMemoryService) Clear(ctx context.Context, userID int64) error {
	m.clearUserID = userID
	m.clearCalled = true
	if m.clearErr != nil {
		return m.clearErr
	}
	m.memories = []*memorydomain.Memory{}
	return nil
}

func (m *mockMemoryService) Delete(ctx context.Context, userID int64, memoryID int64) (bool, error) {
	if m.deleteErr != nil {
		return false, m.deleteErr
	}
	for i, mem := range m.memories {
		if mem.ID == memoryID {
			m.memories = append(m.memories[:i], m.memories[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *mockMemoryService) Update(ctx context.Context, userID int64, memoryID int64, content string) (*memorydomain.Memory, error) {
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	for _, mem := range m.memories {
		if mem.ID == memoryID {
			mem.Content = strings.TrimSpace(content)
			return mem, nil
		}
	}
	return nil, nil
}

func (m *mockMemoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	return nil, nil
}

func newMemoryTestRouter(memorySvc *mockMemoryService) (*gin.Engine, *mockUserService) {
	gin.SetMode(gin.TestMode)
	userSvc := &mockUserService{userID: 42, created: false}
	handler := NewHandler(
		userSvc,
		&mockMessageService{},
		&mockLLMClient{},
		&mockLLMConfigService{},
		memorySvc,
		nil,
	)

	router := gin.New()
	router.Use(injectUserID(42))
	router.GET("/api/v1/memories", handler.HandleGetMemories)
	router.POST("/api/v1/memories", handler.HandleCreateMemory)
	router.DELETE("/api/v1/memories", handler.HandleClearMemories)
	router.DELETE("/api/v1/memories/:id", handler.HandleDeleteMemory)
	router.PUT("/api/v1/memories/:id", handler.HandleUpdateMemory)
	return router, userSvc
}

func TestHandleGetMemories_WithMemories(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{
		{
			ID:        1,
			UserID:    42,
			Content:   "我偏好简洁直接的回答",
			CreatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		},
	}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"memories"`)
	assert.Contains(t, w.Body.String(), `"id":1`)
	assert.Contains(t, w.Body.String(), "我偏好简洁直接的回答")
	assert.Equal(t, int64(42), memorySvc.listUserID)
}

func TestHandleGetMemories_Empty(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"memories":[]`)
}

func TestHandleGetMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{listErr: errors.New("db down")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "db down")
}

func TestHandleCreateMemory_Success(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"content":" 我偏好简洁直接的回答 "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"message":"已记住。"`)
	assert.Contains(t, w.Body.String(), "我偏好简洁直接的回答")
	assert.Equal(t, int64(42), memorySvc.rememberUserID)
	assert.Equal(t, " 我偏好简洁直接的回答 ", memorySvc.rememberContent)
}

func TestHandleCreateMemory_EmptyContent(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: memorysvc.ErrEmptyContent}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"content":"   "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入要长期记住的内容。")
}

func TestHandleCreateMemory_ContentTooLong(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: memorysvc.ErrContentTooLong}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"content":"too long"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "这条记忆太长了，请控制在 200 字以内。")
}

func TestHandleCreateMemory_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: errors.New("insert failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"content":"我偏好简洁回答"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "insert failed")
}

func TestHandleClearMemories_Success(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{{ID: 1, UserID: 42, Content: "记忆"}}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已清空你的全部长期记忆。")
	assert.True(t, memorySvc.clearCalled)
	assert.Equal(t, int64(42), memorySvc.clearUserID)
}

func TestHandleClearMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{clearErr: errors.New("delete failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "delete failed")
}

func TestHandleDeleteMemory_Success(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{
		{ID: 1, UserID: 42, Content: "记忆一"},
		{ID: 2, UserID: 42, Content: "记忆二"},
	}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已删除记忆。")
	assert.Len(t, memorySvc.memories, 1)
}

func TestHandleDeleteMemory_NotFound(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories/999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "记忆不存在或不属于当前用户。")
}

func TestHandleDeleteMemory_InvalidID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories/invalid", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "无效的记忆 ID。")
}

func TestHandleDeleteMemory_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{deleteErr: errors.New("delete failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories/1", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
}

func TestHandleUpdateMemory_Success(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{{ID: 1, UserID: 42, Content: "旧内容"}}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPut, "/api/v1/memories/1", strings.NewReader(`{
		"content":" 新内容 "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已更新记忆。")
	assert.Contains(t, w.Body.String(), "新内容")
	assert.Equal(t, "新内容", memorySvc.memories[0].Content)
}

func TestHandleUpdateMemory_NotFound(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPut, "/api/v1/memories/999", strings.NewReader(`{
		"content":"新内容"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "记忆不存在或不属于当前用户。")
}

func TestHandleUpdateMemory_InvalidID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPut, "/api/v1/memories/invalid", strings.NewReader(`{
		"content":"新内容"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "无效的记忆 ID。")
}

func TestHandleUpdateMemory_EmptyContent(t *testing.T) {
	memorySvc := &mockMemoryService{updateErr: memorysvc.ErrEmptyContent}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPut, "/api/v1/memories/1", strings.NewReader(`{
		"content":"   "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入要长期记住的内容。")
}

func TestHandleUpdateMemory_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{updateErr: errors.New("update failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPut, "/api/v1/memories/1", strings.NewReader(`{
		"content":"新内容"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
}

// SearchMemories/SearchDigests 语义检索接口桩(12-记忆系统技术方案):web handler 测试不涉及检索,返回空。
func (m *mockMemoryService) SearchMemories(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.MemoryHit, error) {
	return nil, nil
}

func (m *mockMemoryService) SearchDigests(_ context.Context, _ int64, _ string, _ int) ([]memorydomain.DigestHit, error) {
	return nil, nil
}

// GetMemoryInjection 注入分层桩(web handler 测试不涉及注入,返回空)。
func (m *mockMemoryService) GetMemoryInjection(_ context.Context, _ int64) ([]string, int, error) {
	return nil, 0, nil
}

// ClearSource 按 source 清空桩:记录调用供断言。
func (m *mockMemoryService) ClearSource(_ context.Context, userID int64, source string) error {
	m.clearUserID = userID
	m.clearedSource = source
	return m.clearErr
}

// TestHandleClearMemories_SourceParam ?source=manual|auto → 只清该来源。
func TestHandleClearMemories_SourceParam(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		source      string
		wantSource  string
		wantContain string
		wantStatus  int
	}{
		{name: "清空手动", source: "manual", wantSource: "manual", wantContain: "手动添加", wantStatus: http.StatusOK},
		{name: "清空自动", source: "auto", wantSource: "auto", wantContain: "自动沉淀", wantStatus: http.StatusOK},
		{name: "非法来源 400", source: "hack", wantSource: "", wantContain: "无效", wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockMemoryService{}
			h := &Handler{memoryService: mock}
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/memories?source="+tt.source, nil)
			c.Set("user_id", int64(42))
			c.Params = gin.Params{}

			h.HandleClearMemories(c)

			assert.Equal(t, tt.wantStatus, w.Code)
			assert.Equal(t, tt.wantSource, mock.clearedSource)
			if tt.wantStatus == http.StatusOK {
				assert.Contains(t, w.Body.String(), tt.wantContain)
				assert.Equal(t, int64(42), mock.clearUserID)
			}
		})
	}
}

// TestHandleClearMemories_NoSource 不带 source → 全部清空(兼容渠道语义)。
func TestHandleClearMemories_NoSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mock := &mockMemoryService{}
	h := &Handler{memoryService: mock}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/memories", nil)
	c.Set("user_id", int64(42))

	h.HandleClearMemories(c)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "", mock.clearedSource, "不带 source 应走全量清空")
	assert.Equal(t, int64(42), mock.clearUserID)
}
