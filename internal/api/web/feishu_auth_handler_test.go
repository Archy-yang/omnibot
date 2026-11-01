package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/middleware"
	"omnibot/internal/pkg/auth"
	larkauth "omnibot/internal/service/larkauth"
)

type mockLarkAuth struct {
	started   *startCapture
	startView larkauth.View
	startErr  error
	pollView  larkauth.View
	pollErr   error
	view      larkauth.View
	viewErr   error
	// 19 号补强:健康检查 / 一键重授权
	healthView   larkauth.View
	healthErr    error
	reloginView  larkauth.View
	reloginErr   error
	reloginCalls int
	deletedUID   int64
}

type startCapture struct {
	userID    int64
	appID     string
	appSecret string
}

func (m *mockLarkAuth) StartAuth(ctx context.Context, userID int64, appID, appSecret string) (larkauth.View, error) {
	m.started = &startCapture{userID, appID, appSecret}
	return m.startView, m.startErr
}
func (m *mockLarkAuth) PollAuth(ctx context.Context, userID int64) (larkauth.View, error) {
	return m.pollView, m.pollErr
}
func (m *mockLarkAuth) View(userID int64) (larkauth.View, error) { return m.view, m.viewErr }
func (m *mockLarkAuth) Health(ctx context.Context, userID int64) (larkauth.View, error) {
	if m.healthView.Status != "" {
		return m.healthView, m.healthErr
	}
	return m.view, m.healthErr
}
func (m *mockLarkAuth) Relogin(ctx context.Context, userID int64) (larkauth.View, error) {
	m.reloginCalls++
	return m.reloginView, m.reloginErr
}
func (m *mockLarkAuth) Disconnect(ctx context.Context, userID int64) error {
	m.deletedUID = userID
	return nil
}

func setupFeishuAuthRouter(svc LarkAuthManager, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { // 模拟 AuthRequired:注入 gin.Context(真实中间件同款)
		c.Set(middleware.AuthUserIDKey, userID)
		c.Next()
	})
	h := &Handler{}
	h.SetLarkAuthService(svc)
	r.GET("/api/v1/feishu-auth", h.HandleGetFeishuAuth)
	r.POST("/api/v1/feishu-auth", h.HandleStartFeishuAuth)
	r.GET("/api/v1/feishu-auth/poll", h.HandlePollFeishuAuth)
	r.DELETE("/api/v1/feishu-auth", h.HandleDisconnectFeishuAuth)
	r.POST("/api/v1/feishu-auth/relogin", h.HandleReloginFeishuAuth)
	return r
}

func authReq(method, path, body string) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	return req
}

// T1 GET 状态:透传 view;未授权 → status=none。
func TestHandleGetFeishuAuth(t *testing.T) {
	mock := &mockLarkAuth{view: larkauth.View{Status: "authorized", AppID: "cli_x", UserName: "张三", OpenID: "ou_1"}}
	r := setupFeishuAuthRouter(mock, 7)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/api/v1/feishu-auth", ""))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"authorized"`)
	assert.NotContains(t, w.Body.String(), "secret", "任何响应不得出现 secret 字样")
}

// T2 POST 发起:必填校验 400;成功返回 verify_url/user_code;userID 取自 ctx 而非请求体。
func TestHandleStartFeishuAuth(t *testing.T) {
	mock := &mockLarkAuth{startView: larkauth.View{Status: "pending", VerifyURL: "https://v", UserCode: "CODE1"}}
	r := setupFeishuAuthRouter(mock, 7)

	body := `{"app_id":"cli_x","app_secret":"s1"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", body))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, mock.started)
	assert.Equal(t, int64(7), mock.started.userID)
	assert.Equal(t, "cli_x", mock.started.appID)
	assert.Equal(t, "s1", mock.started.appSecret)
	assert.Contains(t, w.Body.String(), "https://v")
	assert.Contains(t, w.Body.String(), "CODE1")

	// 缺字段 → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", `{"app_id":"cli_x"}`))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// service 拒绝(如加密未初始化) → 400/500 透传语义
	mock2 := &mockLarkAuth{startErr: larkauth.ErrInvalidInput}
	r2 := setupFeishuAuthRouter(mock2, 7)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", body))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// T3 GET poll:透传;DELETE:按 ctx userID 删。
func TestHandlePollAndDisconnect(t *testing.T) {
	mock := &mockLarkAuth{pollView: larkauth.View{Status: "pending", VerifyURL: "https://v"}}
	r := setupFeishuAuthRouter(mock, 7)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/api/v1/feishu-auth/poll", ""))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"pending"`)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodDelete, "/api/v1/feishu-auth", ""))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), mock.deletedUID)
}

// T4 无用户上下文(中间件未注入)→ 401 语义由中间件保证,handler 侧兜底 401。
func TestHandleFeishuAuth_NoUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetLarkAuthService(&mockLarkAuth{view: larkauth.View{Status: "none"}})
	r.GET("/api/v1/feishu-auth", h.HandleGetFeishuAuth)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/feishu-auth", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// T5 真实链路回归:JWT 中间件注入的是 gin.Context(user_id),handler 必须从同源读取——
// 曾误读 request ctx 导致有效 token 也 401,前端被踢回登录页。
func TestHandleFeishuAuth_WithRealJWTMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	jwtSvc := auth.NewJWTService("test-secret", time.Hour)
	tok, err := jwtSvc.GenerateToken(7)
	require.NoError(t, err)

	mock := &mockLarkAuth{view: larkauth.View{Status: "none"}}
	r := gin.New()
	h := &Handler{}
	h.SetLarkAuthService(mock)
	authorized := r.Group("/", middleware.AuthRequired(jwtSvc))
	authorized.GET("/api/v1/feishu-auth", h.HandleGetFeishuAuth)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/feishu-auth", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code, "有效 JWT 不应被 handler 拒绝: %s", w.Body.String())
	assert.Contains(t, w.Body.String(), `"status":"none"`)
}

// T5 GET 状态走健康检查:CLI 真实态为 need_relogin 时,web 必须显示需要重新授权。
func TestHandleGetFeishuAuth_UsesHealthCheck(t *testing.T) {
	mock := &mockLarkAuth{healthView: larkauth.View{Status: "need_relogin", AppID: "cli_x", UserName: "杨琦"}}
	r := setupFeishuAuthRouter(mock, 7)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/api/v1/feishu-auth", ""))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"need_relogin"`)
}

// T6 一键重授权:成功返回 pending + 验证链接。
func TestHandleReloginFeishuAuth(t *testing.T) {
	mock := &mockLarkAuth{reloginView: larkauth.View{
		Status: "pending", VerifyURL: "https://accounts.feishu.cn/verify?x=1", UserCode: "CODE-9",
	}}
	r := setupFeishuAuthRouter(mock, 7)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth/relogin", ""))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, 1, mock.reloginCalls)
	assert.Contains(t, w.Body.String(), `"status":"pending"`)
	assert.Contains(t, w.Body.String(), "https://accounts.feishu.cn/verify?x=1")
}

// T7 一键重授权:无历史记录 → 404(提示先连接一次)。
func TestHandleReloginFeishuAuth_NoRecord(t *testing.T) {
	mock := &mockLarkAuth{reloginErr: larkauth.ErrNotAuthorized}
	r := setupFeishuAuthRouter(mock, 7)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth/relogin", ""))

	assert.Equal(t, http.StatusNotFound, w.Code)
}
