package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omnibot/internal/middleware"
	memorydomain "omnibot/internal/domain/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// style-profile handler 测试(24号 M1):契约 + 归属隔离(404)。

type mockStyleProfile struct {
	view     *memorysvc.StyleProfileView
	delOK    bool
	injectOn bool
	setErr   error
}

func (m *mockStyleProfile) StyleProfile(int64, int) (*memorysvc.StyleProfileView, error) {
	return m.view, nil
}
func (m *mockStyleProfile) DeleteStyleObservation(int64, int64) (bool, error) { return m.delOK, nil }
func (m *mockStyleProfile) DeleteCognitionChange(int64, int64) (bool, error)  { return m.delOK, nil }
func (m *mockStyleProfile) GetStyleInjection(_ int64) (bool, error) { return m.injectOn, nil }
func (m *mockStyleProfile) SetStyleInjection(_ int64, enabled bool) error {
	m.injectOn = enabled
	return m.setErr
}

func setupStyleProfileRouter(svc StyleProfileService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetStyleProfileService(svc)
	r.GET("/api/v1/style-profile", h.HandleGetStyleProfile)
	r.PUT("/api/v1/style-profile/injection", h.HandleUpdateStyleInjection)
	r.DELETE("/api/v1/style-profile/observation/:id", h.HandleDeleteStyleObservation)
	r.DELETE("/api/v1/style-profile/change/:id", h.HandleDeleteCognitionChange)
	return r
}

func TestStyleProfileAPI(t *testing.T) {
	mock := &mockStyleProfile{view: &memorysvc.StyleProfileView{
		Observations: []memorysvc.StyleObservationView{{
			ID: 1, Dimension: memorydomain.DimensionTone, Content: "直接", CreatedAt: time.Now(),
		}},
		Changes: []memorysvc.CognitionChangeView{{ID: 2, Content: "从 X 改为 Y", CreatedAt: time.Now()}},
	}}
	r := setupStyleProfileRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/style-profile", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"dimension":"tone"`)
	assert.Contains(t, w.Body.String(), "从 X 改为 Y")

	// 删除命中 200 / 未命中 404
	r2 := setupStyleProfileRouter(&mockStyleProfile{delOK: true})
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/observation/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/change/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	r3 := setupStyleProfileRouter(&mockStyleProfile{})
	w = httptest.NewRecorder()
	r3.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/observation/1", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// M2 注入开关:GET 视图带回 injection_enabled;PUT 写开关(归属取自 auth)。
func TestStyleProfileInjectionAPI(t *testing.T) {
	mock := &mockStyleProfile{injectOn: false}
	r := setupStyleProfileRouter(mock)

	// GET:视图带 injection_enabled
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/style-profile", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"injection_enabled":false`)

	// PUT 开启
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/style-profile/injection",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.injectOn, "开关应写为 true")

	// PUT 后 GET 回显 true
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/style-profile", nil))
	assert.Contains(t, w.Body.String(), `"injection_enabled":true`)

	// 非法请求体 400
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/style-profile/injection",
		strings.NewReader(`{bad json}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// service 错误 500
	r2 := setupStyleProfileRouter(&mockStyleProfile{setErr: errors.New("db down")})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/style-profile/injection",
		strings.NewReader(`{"enabled":true}`))
	req.Header.Set("Content-Type", "application/json")
	r2.ServeHTTP(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
