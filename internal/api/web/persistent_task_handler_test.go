package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/middleware"
	"omnibot/internal/pkg/auth"
	agentsvc "omnibot/internal/service/agent"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// persistent-tasks handler 测试(25 号 T7):契约 + 错误映射(404/401)+ 归属隔离。

type mockPersistentTasks struct {
	list    []*agentdomain.PersistentTask
	ctrlErr error
	paused  bool
	resumed bool
	deleted bool
}

func (m *mockPersistentTasks) List(int64) ([]*agentdomain.PersistentTask, error) {
	return m.list, nil
}
func (m *mockPersistentTasks) Pause(int64, int64) error  { m.paused = true; return m.ctrlErr }
func (m *mockPersistentTasks) Resume(int64, int64) error { m.resumed = true; return m.ctrlErr }
func (m *mockPersistentTasks) Delete(int64, int64) error { m.deleted = true; return m.ctrlErr }

func setupPersistentTasksRouter(svc PersistentTaskAPIService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetPersistentTaskService(svc)
	r.GET("/api/v1/persistent-tasks", h.HandleListPersistentTasks)
	r.POST("/api/v1/persistent-tasks/:id/:action", h.HandleControlPersistentTask)
	r.DELETE("/api/v1/persistent-tasks/:id", h.HandleDeletePersistentTask)
	return r
}

func TestPersistentTasksAPI(t *testing.T) {
	next := time.Now().Add(time.Hour)
	mock := &mockPersistentTasks{list: []*agentdomain.PersistentTask{{
		ID: 7, Name: "抓资讯", Goal: "抓 AI 资讯", Cron: "0 9 * * 1",
		Enabled: true, NextRunAt: next, FailStreak: 0, CreatedAt: time.Now(),
	}}}
	r := setupPersistentTasksRouter(mock)

	// list:视图字段不泄露 TaskSpecJSON
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/persistent-tasks", nil))
	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `"name":"抓资讯"`)
	assert.Contains(t, body, `"next_run_at"`)
	assert.NotContains(t, body, "TaskSpecJSON", "内部合同字段不得外泄")

	// pause
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/persistent-tasks/7/pause", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.paused)

	// resume
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/persistent-tasks/7/resume", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.resumed)

	// 未知 action → 404
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/persistent-tasks/7/frobnicate", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// delete
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/persistent-tasks/7", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.deleted)
}

func TestPersistentTasksAPI_Errors(t *testing.T) {
	// 未命中/他人任务 → 404(归属隔离统一 not found,防存在性泄露)
	mock := &mockPersistentTasks{ctrlErr: agentsvc.ErrPersistentTaskNotFound}
	r := setupPersistentTasksRouter(mock)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/persistent-tasks/99/pause", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/persistent-tasks/99", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 内部错误 → 500(不泄露细节)
	mock2 := &mockPersistentTasks{ctrlErr: errors.New("db down")}
	r2 := setupPersistentTasksRouter(mock2)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/persistent-tasks/1/resume", nil))
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	// 无 token → 401(真实 AuthRequired)
	jwtSvc := auth.NewJWTService("test-secret", time.Hour)
	gin.SetMode(gin.TestMode)
	r3 := gin.New()
	h := &Handler{}
	h.SetPersistentTaskService(&mockPersistentTasks{})
	r3.GET("/api/v1/persistent-tasks", middleware.AuthRequired(jwtSvc), h.HandleListPersistentTasks)
	w = httptest.NewRecorder()
	r3.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/persistent-tasks", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
