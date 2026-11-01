package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/middleware"
	wechatclawsvc "omnibot/internal/service/wechatclaw"
)

// mockWeChatClawService 记录调用并返回预设结果。
type mockWeChatClawService struct {
	loginView  *wechatclawsvc.LoginView
	loginErr   error
	pollView   *wechatclawsvc.PollView
	pollConn   bool
	pollErr    error
	statusView *wechatclawsvc.StatusView
	statusErr  error
	discErr    error
	pollQrcode string
	pollVerify string
	pollUserID int64
}

func (m *mockWeChatClawService) StartLogin(ctx context.Context) (*wechatclawsvc.LoginView, error) {
	return m.loginView, m.loginErr
}

func (m *mockWeChatClawService) PollLogin(ctx context.Context, qrcode, verifyCode string, loginUserID int64) (*wechatclawsvc.PollView, bool, error) {
	m.pollQrcode, m.pollVerify = qrcode, verifyCode
	m.pollUserID = loginUserID
	return m.pollView, m.pollConn, m.pollErr
}

func (m *mockWeChatClawService) Status() (*wechatclawsvc.StatusView, error) {
	return m.statusView, m.statusErr
}

func (m *mockWeChatClawService) Disconnect(ctx context.Context) error {
	return m.discErr
}

func newWeChatClawRouter(svc WeChatClawService, withAuth bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewWeChatClawHandler(svc)
	g := r.Group("/api/v1/wechat-claw")
	if withAuth {
		g.Use(func(c *gin.Context) {
			c.Set(middleware.AuthUserIDKey, int64(7))
			c.Next()
		})
	}
	g.POST("/login", h.HandleStartLogin)
	g.GET("/login/:qrcode", h.HandlePollLogin)
	g.GET("/status", h.HandleGetStatus)
	g.DELETE("", h.HandleDisconnect)
	return r
}

func TestWeChatClawHandler_StartLogin(t *testing.T) {
	svc := &mockWeChatClawService{loginView: &wechatclawsvc.LoginView{
		QRCode: "qc-1", QRURL: "https://liteapp.weixin.qq.com/q/x", ExpiresIn: 300,
	}}
	r := newWeChatClawRouter(svc, true)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-claw/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"qrcode":"qc-1"`)
	assert.Contains(t, w.Body.String(), `"qr_url":"https://liteapp.weixin.qq.com/q/x"`)
}

func TestWeChatClawHandler_StartLoginUnavailable(t *testing.T) {
	r := newWeChatClawRouter(nil, true)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-claw/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestWeChatClawHandler_RequiresAuth(t *testing.T) {
	r := newWeChatClawRouter(&mockWeChatClawService{}, false)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-claw/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWeChatClawHandler_PollLoginPassesQuery(t *testing.T) {
	svc := &mockWeChatClawService{
		pollView: &wechatclawsvc.PollView{Status: "confirmed", Connected: true},
		pollConn: true,
	}
	r := newWeChatClawRouter(svc, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-claw/login/qc-1?verify_code=8888", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "qc-1", svc.pollQrcode)
	assert.Equal(t, "8888", svc.pollVerify)
	assert.Equal(t, int64(7), svc.pollUserID)
	assert.Contains(t, w.Body.String(), `"connected":true`)
}

func TestWeChatClawHandler_PollLoginExpired(t *testing.T) {
	svc := &mockWeChatClawService{pollErr: wechatclawsvc.ErrQRExpired}
	r := newWeChatClawRouter(svc, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-claw/login/ghost", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusGone, w.Code)
	assert.Contains(t, w.Body.String(), `"code":"qrcode_expired"`)
}

func TestWeChatClawHandler_Status(t *testing.T) {
	svc := &mockWeChatClawService{statusView: &wechatclawsvc.StatusView{
		Connected: true, BotID: "bot-1", Status: "active",
	}}
	r := newWeChatClawRouter(svc, true)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-claw/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"connected":true`)
	assert.Contains(t, w.Body.String(), `"bot_id":"bot-1"`)
}

func TestWeChatClawHandler_Disconnect(t *testing.T) {
	r := newWeChatClawRouter(&mockWeChatClawService{}, true)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/wechat-claw", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	errRouter := newWeChatClawRouter(&mockWeChatClawService{discErr: errors.New("boom")}, true)
	req2 := httptest.NewRequest(http.MethodDelete, "/api/v1/wechat-claw", nil)
	w2 := httptest.NewRecorder()
	errRouter.ServeHTTP(w2, req2)
	assert.Equal(t, http.StatusInternalServerError, w2.Code)
}
