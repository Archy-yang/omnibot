package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/middleware"
	searchsvc "omnibot/internal/service/search"
)

// N7 API 层:鉴权、掩码回显、留空保持、校验错误返回 400。

type mockSearchConfigService struct {
	view       *searchsvc.ConfigView
	getErr     error
	saved      *searchsvc.SaveInput
	savedUser  int64
	saveErr    error
	cleared    int64
	clearErr   error
}

func (m *mockSearchConfigService) GetView(userID int64) (*searchsvc.ConfigView, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.view == nil {
		m.view = &searchsvc.ConfigView{Enabled: true, Provider: "deepseek"}
	}
	return m.view, nil
}

func (m *mockSearchConfigService) SaveConfig(userID int64, in searchsvc.SaveInput) (*searchsvc.ConfigView, error) {
	m.savedUser = userID
	m.saved = &in
	if m.saveErr != nil {
		return nil, m.saveErr
	}
	return &searchsvc.ConfigView{HasConfig: true, APIKeyMasked: "sk-ab...89"}, nil
}

func (m *mockSearchConfigService) ClearConfig(userID int64) error {
	m.cleared = userID
	return m.clearErr
}

func setupSearchConfigRouter(svc SearchConfigService, userID int64) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if userID > 0 {
			c.Set(middleware.AuthUserIDKey, userID)
		}
		c.Next()
	})
	h := &Handler{}
	h.SetSearchConfigService(svc)
	r.GET("/api/v1/user/search-config", h.HandleGetSearchConfig)
	r.PUT("/api/v1/user/search-config", h.HandleUpdateSearchConfig)
	r.DELETE("/api/v1/user/search-config", h.HandleDeleteSearchConfig)
	return r
}

func TestHandleGetSearchConfig(t *testing.T) {
	svc := &mockSearchConfigService{view: &searchsvc.ConfigView{
		HasConfig: true, Enabled: true, Provider: "deepseek",
		BaseURL: "https://api.deepseek.com", APIKeyMasked: "sk-ab...89",
		DefaultCount: 5, TimeoutSeconds: 20,
	}}
	r := setupSearchConfigRouter(svc, 1)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/user/search-config", nil))

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "sk-ab...89")
	assert.NotContains(t, body, "sk-plain", "绝不能回显明文")
}

func TestHandleGetSearchConfig_Unauthorized(t *testing.T) {
	r := setupSearchConfigRouter(&mockSearchConfigService{}, 0)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/user/search-config", nil))

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestHandleUpdateSearchConfig_ParsesBody(t *testing.T) {
	svc := &mockSearchConfigService{}
	r := setupSearchConfigRouter(svc, 7)

	body := `{"base_url":"https://proxy.example.com","api_key":"sk-new-key","enabled":false,
	          "default_count":8,"need_content":true,"timeout_seconds":30}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/search-config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, svc.saved)
	assert.Equal(t, int64(7), svc.savedUser, "用户身份只来自 JWT")
	assert.Equal(t, "sk-new-key", svc.saved.APIKey)
	assert.Equal(t, "https://proxy.example.com", svc.saved.BaseURL)
	require.NotNil(t, svc.saved.Enabled)
	assert.False(t, *svc.saved.Enabled)
	assert.Equal(t, 8, svc.saved.DefaultCount)
	require.NotNil(t, svc.saved.NeedContent)
	assert.True(t, *svc.saved.NeedContent)
	assert.Equal(t, 30, svc.saved.TimeoutSeconds)
}

func TestHandleUpdateSearchConfig_ClearKey(t *testing.T) {
	svc := &mockSearchConfigService{}
	r := setupSearchConfigRouter(svc, 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/search-config",
		strings.NewReader(`{"clear_key":true,"api_key":"sk-should-be-ignored"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, svc.saved.ClearKey)
}

func TestHandleUpdateSearchConfig_ValidationError(t *testing.T) {
	svc := &mockSearchConfigService{saveErr: errors.New("每次条数应在 1~20 之间")}
	r := setupSearchConfigRouter(svc, 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/search-config",
		strings.NewReader(`{"default_count":99}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "条数")
}

func TestHandleUpdateSearchConfig_BadJSON(t *testing.T) {
	r := setupSearchConfigRouter(&mockSearchConfigService{}, 1)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/user/search-config", strings.NewReader(`{oops`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleDeleteSearchConfig(t *testing.T) {
	svc := &mockSearchConfigService{}
	r := setupSearchConfigRouter(svc, 3)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/user/search-config", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(3), svc.cleared)
}
