package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	agentpkg "omnibot/internal/service/agent"
	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// webMockRunner web 包测试用 SubAgentRunner stub。report 测试不调 StartTask,
// runner 仅用于构造 SubAgentService,不会被实际调用。
type webMockRunner struct{}

func (w *webMockRunner) Run(_ context.Context, _ int64, _ domainagent.SubAgentCard, _ string) (string, []agentpkg.StepRecord, error) {
	return "r", nil, nil
}

func setupAgentTaskHandlerTest(t *testing.T) (*AgentTaskHandler, repoagent.AgentTaskRepository) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}))

	repo := repoagent.NewAgentTaskRepository(db)
	registry := agentpkg.NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "查阅资料",
		PromptTemplate: "p", Tools: []string{}, MaxSteps: 10, Timeout: 5000000000,
	}))
	svc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, chatrepo.NewAgentStepRepository(db))
	handler := NewAgentTaskHandler(svc, &mockAgentService{}, registry, &mockLLMConfigService{hasConfig: false})
	return handler, repo
}

func TestHandleListTasks_CompletedUnreported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)

	art := "result"
	t1 := domainagent.NewAgentTask(42, "researcher", "g1")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))
	t2 := domainagent.NewAgentTask(42, "researcher", "g2")
	require.NoError(t, repo.Create(t2))
	require.NoError(t, repo.UpdateStatus(t2.ID, domainagent.TaskStatusCompleted, &art, nil))
	require.NoError(t, repo.MarkReported(t2.ID))

	router := gin.New()
	router.Use(injectUserID(42))
	router.GET("/api/v1/agent/tasks", handler.HandleListTasks)

	req, _ := http.NewRequest("GET", "/api/v1/agent/tasks?status=completed_unreported", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Tasks []map[string]interface{} `json:"tasks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	require.Len(t, resp.Data.Tasks, 1)
	assert.Equal(t, float64(t1.ID), resp.Data.Tasks[0]["id"])
}

func TestHandleListTasks_UserIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)

	art := "r"
	t1 := domainagent.NewAgentTask(1, "researcher", "g1")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))
	t2 := domainagent.NewAgentTask(2, "researcher", "g2")
	require.NoError(t, repo.Create(t2))
	require.NoError(t, repo.UpdateStatus(t2.ID, domainagent.TaskStatusCompleted, &art, nil))

	router := gin.New()
	router.Use(injectUserID(1))
	router.GET("/api/v1/agent/tasks", handler.HandleListTasks)
	req, _ := http.NewRequest("GET", "/api/v1/agent/tasks?status=completed_unreported", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var resp struct {
		Data struct {
			Tasks []map[string]interface{} `json:"tasks"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Data.Tasks, 1)
	assert.Equal(t, float64(t1.ID), resp.Data.Tasks[0]["id"])
}

func TestHandleReportTask_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _ := setupAgentTaskHandlerTest(t)

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/9999/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleReportTask_ForbiddenNotOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	t1 := domainagent.NewAgentTask(1, "researcher", "g")
	require.NoError(t, repo.Create(t1))
	art := "r"
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))

	router := gin.New()
	router.Use(injectUserID(2))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleReportTask_AlreadyReported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	t1 := domainagent.NewAgentTask(42, "researcher", "g")
	require.NoError(t, repo.Create(t1))
	art := "r"
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))
	require.NoError(t, repo.MarkReported(t1.ID))

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestHandleReportTask_NotCompleted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)
	t1 := domainagent.NewAgentTask(42, "researcher", "g") // pending 状态
	require.NoError(t, repo.Create(t1))

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}
