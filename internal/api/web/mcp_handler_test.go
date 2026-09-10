package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMCPManager struct {
	views         []skilldomain.ServerView
	addName       string
	addURL        string
	addKey        string
	addEnabled    bool
	addErr        error
	updatedID     int64
	deletedID     int64
	syncedID      int64
	syncResult    *skillsvc.SyncResult
	syncErr       error
	updateErr     error
	listErr       error
	authorizedID  int64
	authorizeErr  error
	callbackErr   error
	callbackCode  string
	callbackState string
}

func (m *mockMCPManager) ListServers() ([]skilldomain.ServerView, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.views, nil
}

func (m *mockMCPManager) AddServer(in skillsvc.MCPServerInput) (*skilldomain.ServerView, error) {
	m.addName, m.addURL, m.addKey, m.addEnabled = in.Name, in.BaseURL, in.APIKey, in.Enabled
	if m.addErr != nil {
		return nil, m.addErr
	}
	return &skilldomain.ServerView{ID: 1, Name: in.Name, BaseURL: in.BaseURL, Enabled: in.Enabled, HasAPIKey: in.APIKey != "", ToolCount: 2}, nil
}

func (m *mockMCPManager) UpdateServer(id int64, in skillsvc.MCPServerInput) (*skilldomain.ServerView, error) {
	m.updatedID = id
	if m.updateErr != nil {
		return nil, m.updateErr
	}
	return &skilldomain.ServerView{ID: id, Name: in.Name, BaseURL: in.BaseURL, Enabled: in.Enabled}, nil
}

func (m *mockMCPManager) BeginOAuth(ctx context.Context, id int64) (*skillsvc.OAuthBeginResult, error) {
	m.authorizedID = id
	if m.authorizeErr != nil {
		return nil, m.authorizeErr
	}
	return &skillsvc.OAuthBeginResult{AuthorizationURL: "https://auth.example.com/authorize?client_id=c", State: "st-1"}, nil
}

func (m *mockMCPManager) HandleOAuthCallback(ctx context.Context, code, state string) error {
	if m.callbackErr != nil {
		return m.callbackErr
	}
	m.callbackCode, m.callbackState = code, state
	return nil
}

func (m *mockMCPManager) DeleteServer(id int64) error {
	m.deletedID = id
	return nil
}

func (m *mockMCPManager) SyncServer(id int64) (*skillsvc.SyncResult, error) {
	m.syncedID = id
	if m.syncErr != nil {
		return nil, m.syncErr
	}
	if m.syncResult == nil {
		m.syncResult = &skillsvc.SyncResult{ServerName: "github", ToolCount: 5}
	}
	return m.syncResult, nil
}

func setupMCPRouter(mgr MCPManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetMCPManager(mgr)
	g := r.Group("/api/v1/mcp")
	g.GET("/servers", h.HandleListMCPServers)
	g.POST("/servers", h.HandleCreateMCPServer)
	g.PUT("/servers/:id", h.HandleUpdateMCPServer)
	g.DELETE("/servers/:id", h.HandleDeleteMCPServer)
	g.POST("/servers/:id/sync", h.HandleSyncMCPServer)
	g.POST("/servers/:id/authorize", h.HandleAuthorizeMCPServer)
	g.GET("/oauth/callback", h.HandleOAuthCallback)
	return r
}

func TestHandleListMCPServers(t *testing.T) {
	mgr := &mockMCPManager{views: []skilldomain.ServerView{
		{ID: 1, Name: "github", BaseURL: "https://x.com", Enabled: true, HasAPIKey: true, ToolCount: 3},
	}}
	r := setupMCPRouter(mgr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/servers", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Servers []skilldomain.ServerView `json:"servers"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Servers, 1)
	assert.Equal(t, "github", resp.Data.Servers[0].Name)
	assert.True(t, resp.Data.Servers[0].HasAPIKey)
}

func TestHandleCreateMCPServer(t *testing.T) {
	mgr := &mockMCPManager{}
	r := setupMCPRouter(mgr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers",
		strings.NewReader(`{"name":"github","base_url":"https://x.com/mcp","api_key":"sk-1","enabled":true}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "github", mgr.addName)
	assert.Equal(t, "sk-1", mgr.addKey)

	// 缺 name → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers",
		strings.NewReader(`{"base_url":"https://x.com"}`)))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 业务校验失败(重名等) → 400 + 可读信息
	mgr.addErr = assert.AnError
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers",
		strings.NewReader(`{"name":"dup","base_url":"https://x.com"}`)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUpdateMCPServer(t *testing.T) {
	mgr := &mockMCPManager{}
	r := setupMCPRouter(mgr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/mcp/servers/7",
		strings.NewReader(`{"name":"github","base_url":"https://new.com","api_key":"","enabled":false}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), mgr.updatedID)

	// 非法 id → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/mcp/servers/abc",
		strings.NewReader(`{"name":"x","base_url":"https://x.com","enabled":true}`)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleDeleteAndSyncMCPServer(t *testing.T) {
	mgr := &mockMCPManager{}
	r := setupMCPRouter(mgr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/mcp/servers/7", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), mgr.deletedID)

	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers/7/sync", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), mgr.syncedID)

	var resp struct {
		Data struct {
			ToolCount int    `json:"tool_count"`
			Err       string `json:"err"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, 5, resp.Data.ToolCount)
}

func TestHandleAuthorizeMCPServer(t *testing.T) {
	mgr := &mockMCPManager{}
	r := setupMCPRouter(mgr)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers/9/authorize", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(9), mgr.authorizedID)

	var resp struct {
		Data struct {
			AuthorizationURL string `json:"authorization_url"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp.Data.AuthorizationURL, "authorize?")

	// 业务失败 → 400
	mgr.authorizeErr = assert.AnError
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/mcp/servers/9/authorize", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleOAuthCallback(t *testing.T) {
	mgr := &mockMCPManager{}
	r := setupMCPRouter(mgr)

	// 缺参 → 400
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/oauth/callback", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 正常回调
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/oauth/callback?code=c1&state=st-9", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "c1", mgr.callbackCode)
	assert.Equal(t, "st-9", mgr.callbackState)

	// state 校验失败 → 400 + 可读信息
	mgr.callbackErr = assert.AnError
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/mcp/oauth/callback?code=c2&state=st-9", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
