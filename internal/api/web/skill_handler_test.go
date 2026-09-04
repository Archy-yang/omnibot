package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSkillService struct {
	views         []skillsvc.SkillView
	listErr       error
	enabledName   string
	enabledValue  bool
	setEnabledErr error
}

func (m *mockSkillService) List() ([]skillsvc.SkillView, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.views, nil
}

func (m *mockSkillService) SetEnabled(name string, enabled bool) error {
	if m.setEnabledErr != nil {
		return m.setEnabledErr
	}
	m.enabledName = name
	m.enabledValue = enabled
	return nil
}

func setupSkillRouter(svc SkillService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetSkillService(svc)
	r.GET("/api/v1/skills", h.HandleListSkills)
	r.PUT("/api/v1/skills/:name", h.HandleUpdateSkill)
	return r
}

func TestHandleListSkills(t *testing.T) {
	mock := &mockSkillService{views: []skillsvc.SkillView{
		{Name: "calculator", DisplayName: "计算了一下", Description: "d", Source: "builtin", Enabled: true, Available: true},
		{Name: "rss_reader", DisplayName: "读取了 RSS", Source: "builtin", Enabled: false, Available: true},
	}}
	r := setupSkillRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Skills []skillsvc.SkillView `json:"skills"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Skills, 2)
	assert.Equal(t, "calculator", resp.Data.Skills[0].Name)
	assert.True(t, resp.Data.Skills[0].Enabled)
	assert.False(t, resp.Data.Skills[1].Enabled)
}

func TestHandleListSkills_ServiceError(t *testing.T) {
	mock := &mockSkillService{listErr: assert.AnError}
	r := setupSkillRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/skills", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestHandleUpdateSkill_EnableAndDisable(t *testing.T) {
	mock := &mockSkillService{}
	r := setupSkillRouter(mock)

	// 停用
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/skills/calculator",
		strings.NewReader(`{"enabled":false}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "calculator", mock.enabledName)
	assert.False(t, mock.enabledValue)

	// 开启
	mock.enabledName = ""
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/skills/calculator",
		strings.NewReader(`{"enabled":true}`)))
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.enabledValue)

	// 非法 body → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/skills/calculator",
		strings.NewReader(`{"enabled":"yes"}`)))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleUpdateSkill_ServiceError(t *testing.T) {
	mock := &mockSkillService{setEnabledErr: assert.AnError}
	r := setupSkillRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/api/v1/skills/calculator",
		strings.NewReader(`{"enabled":true}`)))
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
