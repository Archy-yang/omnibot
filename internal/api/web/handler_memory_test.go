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

func newMemoryTestRouter(memorySvc *mockMemoryService) (*gin.Engine, *mockUserService) {
	gin.SetMode(gin.TestMode)
	userSvc := &mockUserService{userID: 42, created: false}
	handler := NewHandler(
		userSvc,
		&mockMessageService{},
		&mockLLMClient{},
		&mockLLMConfigService{},
		memorySvc,
	)

	router := gin.New()
	router.GET("/api/v1/memories", handler.HandleGetMemories)
	router.POST("/api/v1/memories", handler.HandleCreateMemory)
	router.DELETE("/api/v1/memories", handler.HandleClearMemories)
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
	router, userSvc := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"memories"`)
	assert.Contains(t, w.Body.String(), `"id":1`)
	assert.Contains(t, w.Body.String(), "我偏好简洁直接的回答")
	assert.Equal(t, "test-session", userSvc.channelID)
	assert.Equal(t, int64(42), memorySvc.listUserID)
}

func TestHandleGetMemories_Empty(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"memories":[]`)
}

func TestHandleGetMemories_MissingSessionID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "缺少 session_id 参数")
}

func TestHandleGetMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{listErr: errors.New("db down")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
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
		"session_id":"test-session",
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
		"session_id":"test-session",
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
		"session_id":"test-session",
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
		"session_id":"test-session",
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

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已清空你的全部长期记忆。")
	assert.True(t, memorySvc.clearCalled)
	assert.Equal(t, int64(42), memorySvc.clearUserID)
}

func TestHandleClearMemories_MissingSessionID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "缺少 session_id 参数")
	assert.False(t, memorySvc.clearCalled)
}

func TestHandleClearMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{clearErr: errors.New("delete failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "delete failed")
}
