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
)

// mockBindingService 用于 web handler 测试(v2.3 泛化版)
type mockBindingService struct {
	// IsChannelBound 行为:按 channelType 返回
	feishuBound bool
	wechatBound bool
	boundErr    error
	// GenerateCode 行为
	code      string
	expires   time.Time
	genErr    error
	// BindChannel / ResolveUserID(feishu 端用,web 不调,留空实现)
}

func (m *mockBindingService) GenerateCode(userID int64) (string, time.Time, error) {
	return m.code, m.expires, m.genErr
}

func (m *mockBindingService) IsChannelBound(userID int64, channelType string) (bool, error) {
	if m.boundErr != nil {
		return false, m.boundErr
	}
	switch channelType {
	case "feishu":
		return m.feishuBound, nil
	case "wechat":
		return m.wechatBound, nil
	}
	return false, nil
}

func (m *mockBindingService) BindChannel(channelType, code, openID string) error {
	return nil
}

func (m *mockBindingService) ResolveUserID(channelType, openID string) (int64, bool, error) {
	return 0, false, nil
}

func newChannelBindRouter(svc BindingService) *gin.Engine {
	r := gin.New()
	// 模拟 AuthRequired 注入 user_id=42
	r.Use(func(c *gin.Context) {
		c.Set("user_id", int64(42))
		c.Next()
	})
	h := NewChannelBindHandler(svc)
	g := r.Group("/api/v1/user/channel-binding")
	g.GET("", h.HandleGetBindingStatus)
	g.POST("/bind-code", h.HandleGenerateBindCode)
	return r
}

func TestChannelBindHandler_GetStatus_NeitherBound(t *testing.T) {
	svc := &mockBindingService{feishuBound: false, wechatBound: false}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/channel-binding", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"feishu_bound":false`)
	assert.Contains(t, w.Body.String(), `"wechat_bound":false`)
}

func TestChannelBindHandler_GetStatus_BothBound(t *testing.T) {
	svc := &mockBindingService{feishuBound: true, wechatBound: true}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/channel-binding", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"feishu_bound":true`)
	assert.Contains(t, w.Body.String(), `"wechat_bound":true`)
}

func TestChannelBindHandler_GetStatus_OnlyFeishuBound(t *testing.T) {
	svc := &mockBindingService{feishuBound: true, wechatBound: false}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user/channel-binding", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"feishu_bound":true`)
	assert.Contains(t, w.Body.String(), `"wechat_bound":false`)
}

func TestChannelBindHandler_GenerateCode_Success(t *testing.T) {
	svc := &mockBindingService{
		feishuBound: false,
		wechatBound: false,
		code:        "123456",
		expires:     time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC),
	}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/channel-binding/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"123456"`)
	assert.Contains(t, w.Body.String(), `"expires_at"`)
}

// 已绑飞书、未绑微信:仍可出码(用于绑微信)
func TestChannelBindHandler_GenerateCode_FeishuBoundOnly_StillAllowed(t *testing.T) {
	svc := &mockBindingService{
		feishuBound: true,
		wechatBound: false,
		code:        "654321",
		expires:     time.Now().Add(5 * time.Minute),
	}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/channel-binding/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "已绑飞书但未绑微信,应仍能出码绑微信")
}

// 全渠道都已绑 -> 409
func TestChannelBindHandler_GenerateCode_AllBound_Conflict(t *testing.T) {
	svc := &mockBindingService{
		feishuBound: true,
		wechatBound: true,
	}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/channel-binding/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "你的账号已绑定全部渠道")
}

func TestChannelBindHandler_GenerateCode_InternalError(t *testing.T) {
	svc := &mockBindingService{genErr: errors.New("db down")}
	r := newChannelBindRouter(svc)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/user/channel-binding/bind-code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
