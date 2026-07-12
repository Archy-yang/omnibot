package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userLLM "omnibot/internal/service/user"
)

// mockBindingService 用于 web handler 测试
type mockBindingService struct {
	bound       bool
	boundErr    error
	code        string
	expires     time.Time
	genErr      error
	bindErr     error
	resolveUID  int64
	resolveB    bool
	resolveErr  error
	genCalled   bool
	genGotUID   int64
}

func (m *mockBindingService) GenerateCode(userID int64) (string, time.Time, error) {
	m.genCalled = true
	m.genGotUID = userID
	return m.code, m.expires, m.genErr
}

func (m *mockBindingService) IsFeishuBound(userID int64) (bool, error) {
	return m.bound, m.boundErr
}

func (m *mockBindingService) BindFeishu(code, openID string) error {
	return m.bindErr
}

func (m *mockBindingService) ResolveFeishuUserID(openID string) (int64, bool, error) {
	return m.resolveUID, m.resolveB, m.resolveErr
}

func newFeishuBindRouter(svc BindingService) *gin.Engine {
	r := gin.New()
	// 模拟 AuthRequired 注入 user_id=42
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	})
	h := NewFeishuBindHandler(svc)
	g := r.Group("/api/v1/user/feishu")
	g.GET("/binding", h.HandleGetBindingStatus)
	g.POST("/bind-code", h.HandleGenerateBindCode)
	return r
}

func TestFeishuBindHandler_GetStatus_NotBound(t *testing.T) {
	svc := &mockBindingService{bound: false}
	r := newFeishuBindRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/feishu/binding", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"bound":false`)
}

func TestFeishuBindHandler_GetStatus_Bound(t *testing.T) {
	svc := &mockBindingService{bound: true}
	r := newFeishuBindRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/feishu/binding", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"bound":true`)
}

func TestFeishuBindHandler_GenerateCode_Success(t *testing.T) {
	svc := &mockBindingService{
		bound:   false,
		code:    "123456",
		expires: time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC),
	}
	r := newFeishuBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/feishu/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, svc.genCalled)
	assert.Equal(t, int64(42), svc.genGotUID)
	assert.Contains(t, w.Body.String(), `"code":"123456"`)
	assert.Contains(t, w.Body.String(), `"expires_at"`)
}

func TestFeishuBindHandler_GenerateCode_AlreadyBound(t *testing.T) {
	svc := &mockBindingService{genErr: userLLM.ErrAccountAlreadyBound}
	r := newFeishuBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/feishu/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "你的账号已绑定飞书")
}

func TestFeishuBindHandler_GenerateCode_InternalError(t *testing.T) {
	svc := &mockBindingService{genErr: errors.New("db down")}
	r := newFeishuBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/feishu/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
