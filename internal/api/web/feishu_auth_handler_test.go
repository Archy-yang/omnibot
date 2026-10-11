package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
	larkauth "omnibot/internal/service/larkauth"
)

type mockLarkAuth struct {
	started    *startCapture
	startView  larkauth.View
	startErr   error
	pollView   larkauth.View
	pollErr    error
	view       larkauth.View
	viewErr    error
	deletedUID int64
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
func (m *mockLarkAuth) Disconnect(ctx context.Context, userID int64) error {
	m.deletedUID = userID
	return nil
}

func setupFeishuAuthRouter(svc LarkAuthManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetLarkAuthService(svc)
	r.GET("/api/v1/feishu-auth", h.HandleGetFeishuAuth)
	r.POST("/api/v1/feishu-auth", h.HandleStartFeishuAuth)
	r.GET("/api/v1/feishu-auth/poll", h.HandlePollFeishuAuth)
	r.DELETE("/api/v1/feishu-auth", h.HandleDisconnectFeishuAuth)
	return r
}

// userID 注入模拟(toolcore.WithUserID 由 JWT 中间件完成,测试直接塞 ctx)
func authReq(method, path, body string, userID int64) *http.Request {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	return req.WithContext(toolcore.WithUserID(req.Context(), userID))
}

// T1 GET 状态:透传 view;未授权 → status=none。
func TestHandleGetFeishuAuth(t *testing.T) {
	mock := &mockLarkAuth{view: larkauth.View{Status: "authorized", AppID: "cli_x", UserName: "张三", OpenID: "ou_1"}}
	r := setupFeishuAuthRouter(mock)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/api/v1/feishu-auth", "", 7))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"authorized"`)
	assert.NotContains(t, w.Body.String(), "secret", "任何响应不得出现 secret 字样")
}

// T2 POST 发起:必填校验 400;成功返回 verify_url/user_code;userID 取自 ctx 而非请求体。
func TestHandleStartFeishuAuth(t *testing.T) {
	mock := &mockLarkAuth{startView: larkauth.View{Status: "pending", VerifyURL: "https://v", UserCode: "CODE1"}}
	r := setupFeishuAuthRouter(mock)

	body := `{"app_id":"cli_x","app_secret":"s1"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", body, 7))
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, mock.started)
	assert.Equal(t, int64(7), mock.started.userID)
	assert.Equal(t, "cli_x", mock.started.appID)
	assert.Equal(t, "s1", mock.started.appSecret)
	assert.Contains(t, w.Body.String(), "https://v")
	assert.Contains(t, w.Body.String(), "CODE1")

	// 缺字段 → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", `{"app_id":"cli_x"}`, 7))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// service 拒绝(如加密未初始化) → 400/500 透传语义
	mock2 := &mockLarkAuth{startErr: larkauth.ErrInvalidInput}
	r2 := setupFeishuAuthRouter(mock2)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, authReq(http.MethodPost, "/api/v1/feishu-auth", body, 7))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// T3 GET poll:透传;DELETE:按 ctx userID 删。
func TestHandlePollAndDisconnect(t *testing.T) {
	mock := &mockLarkAuth{pollView: larkauth.View{Status: "pending", VerifyURL: "https://v"}}
	r := setupFeishuAuthRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodGet, "/api/v1/feishu-auth/poll", "", 7))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"status":"pending"`)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, authReq(http.MethodDelete, "/api/v1/feishu-auth", "", 7))
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
