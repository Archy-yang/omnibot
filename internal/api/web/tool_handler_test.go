package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	toolsvc "omnibot/internal/service/tool"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockToolService struct {
	views         []toolsvc.ToolView
	listErr       error
	enabledName   string
	enabledValue  bool
	setEnabledErr error
}

func (m *mockToolService) List() ([]toolsvc.ToolView, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.views, nil
}

func (m *mockToolService) SetEnabled(name string, enabled bool) error {
	if m.setEnabledErr != nil {
		return m.setEnabledErr
	}
	m.enabledName = name
	m.enabledValue = enabled
	return nil
}

func setupToolRouter(svc ToolService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetToolService(svc)
	r.GET("/api/v1/tools", h.HandleListTools)
	r.PUT("/api/v1/tools/:name", h.HandleUpdateTool)
	return r
}

func TestHandleListTools(t *testing.T) {
	mock := &mockToolService{views: []toolsvc.ToolView{
		{Name: "calculator", DisplayName: "计算了一下", Description: "d", Enabled: true, Available: true},
		{Name: "rss_reader", DisplayName: "读取了 RSS", Enabled: false, Available: true},
	}}
	r := setupToolRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Tools []toolsvc.ToolView `json:"tools"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Tools, 2)
	assert.Equal(t, "calculator", resp.Data.Tools[0].Name)
	assert.True(t, resp.Data.Tools[0].Enabled)
	assert.False(t, resp.Data.Tools[1].Enabled)
}

func TestHandleListTools_ServiceError(t *testing.T) {
	mock := &mockToolService{listErr: assert.AnError}
	r := setupToolRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/tools", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUpdateTool_EnableAndDisable(t *testing.T) {
	mock := &mockToolService{}
	r := setupToolRouter(mock)

	// 停用
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/tools/calculator",
		strings.NewReader(`{"enabled":false}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "calculator", mock.enabledName)
	assert.False(t, mock.enabledValue)

	// 开启
	mock.enabledName = ""
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/tools/calculator",
		strings.NewReader(`{"enabled":true}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.enabledValue)

	// 非法 body → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/tools/calculator",
		strings.NewReader(`{"enabled":"yes"}`)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUpdateTool_ServiceError(t *testing.T) {
	mock := &mockToolService{setEnabledErr: assert.AnError}
	r := setupToolRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/tools/calculator",
		strings.NewReader(`{"enabled":true}`)))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
