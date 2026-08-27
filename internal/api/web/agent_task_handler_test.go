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

	domainagent "omnibot/internal/domain/agent"
	conversation "omnibot/internal/domain/conversation"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
	agentpkg "omnibot/internal/service/agent"
)

// webMockRunner web 包测试用 SubAgentRunner stub。report 测试不调 StartTask,
// runner 仅用于构造 SubAgentService,不会被实际调用。
type webMockRunner struct{}

func (w *webMockRunner) Run(_ context.Context, _ int64, _ int64, _ domainagent.SubAgentCard, _ domainagent.TaskSpec, _ func(agentpkg.StepRecord)) (string, error) {
	return "r", nil
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
		PromptTemplate: "p", MaxSteps: 10, Timeout: 5000000000,
	}))
	svc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, chatrepo.NewAgentStepRepository(db), nil, nil, nil)
	handler := NewAgentTaskHandler(svc, &mockAgentService{}, registry, &mockLLMConfigService{hasConfig: false}, &mockMessageService{})
	return handler, repo
}

func TestHandleListTasks_CompletedUnreported(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo := setupAgentTaskHandlerTest(t)

	art := "result"
	t1 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g1"), "web", "")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))
	t2 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g2"), "web", "")
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
	t1 := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g1"), "web", "")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))
	t2 := domainagent.NewAgentTask(2, "researcher", domainagent.NewTaskSpec("g2"), "web", "")
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
	t1 := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
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
	t1 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
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
	t1 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "") // pending 状态
	require.NoError(t, repo.Create(t1))

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)
}

// setupAgentTaskStepsHandlerTest 同时迁移 agent_steps,返回 stepRepo 供测试直接插步骤验证读取。
func setupAgentTaskStepsHandlerTest(t *testing.T) (*AgentTaskHandler, repoagent.AgentTaskRepository, chatrepo.AgentStepRepository) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &conversation.AgentStep{}))

	repo := repoagent.NewAgentTaskRepository(db)
	stepRepo := chatrepo.NewAgentStepRepository(db)
	registry := agentpkg.NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "查阅资料",
		PromptTemplate: "p", MaxSteps: 10, Timeout: 5000000000,
	}))
	svc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, stepRepo, nil, nil, nil)
	handler := NewAgentTaskHandler(svc, &mockAgentService{}, registry, &mockLLMConfigService{hasConfig: false}, &mockMessageService{})
	return handler, repo, stepRepo
}

func TestHandleListTaskSteps_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo, stepRepo := setupAgentTaskStepsHandlerTest(t)

	t1 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g1"), "web", "")
	require.NoError(t, repo.Create(t1))

	// 插 2 条步骤:llm_call(seq0) + tool_call(seq1)
	taskID := t1.ID
	llmStep := conversation.NewLLMStep(42, `{"role":"user","content":"g1"}`, `{"content":"思考"}`, "gpt-x", "success", 12)
	llmStep.TaskID = &taskID
	llmStep.Seq = 0
	toolStep := conversation.NewToolStep(42, "rss_reader", `{"url":"x"}`, `{"items":[]}`, "success", 5)
	toolStep.TaskID = &taskID
	toolStep.Seq = 1
	require.NoError(t, stepRepo.CreateBatch([]*conversation.AgentStep{llmStep, toolStep}))

	router := gin.New()
	router.Use(injectUserID(42))
	router.GET("/api/v1/agent/tasks/:id/steps", handler.HandleListTaskSteps)
	req, _ := http.NewRequest("GET", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/steps", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Steps []map[string]interface{} `json:"steps"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Success)
	require.Len(t, resp.Data.Steps, 2, "应返回插入的 2 条步骤")
	assert.Equal(t, "llm_call", resp.Data.Steps[0]["kind"], "按 seq 正序,llm_call 在前")
	assert.Equal(t, "tool_call", resp.Data.Steps[1]["kind"])
	assert.Equal(t, "rss_reader", resp.Data.Steps[1]["tool"])
}

func TestHandleListTaskSteps_ForbiddenNotOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, repo, _ := setupAgentTaskStepsHandlerTest(t)

	t1 := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "") // 属主 user 1
	require.NoError(t, repo.Create(t1))

	router := gin.New()
	router.Use(injectUserID(2)) // 请求者 user 2
	router.GET("/api/v1/agent/tasks/:id/steps", handler.HandleListTaskSteps)
	req, _ := http.NewRequest("GET", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/steps", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestHandleListTaskSteps_NotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler, _, _ := setupAgentTaskStepsHandlerTest(t)

	router := gin.New()
	router.Use(injectUserID(42))
	router.GET("/api/v1/agent/tasks/:id/steps", handler.HandleListTaskSteps)
	req, _ := http.NewRequest("GET", "/api/v1/agent/tasks/9999/steps", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

// setupReportHandlerTest 构造带真实 taskRepo/stepRepo + mock MessageService 的 handler,
// 验证 HandleReportTask 落库汇报消息。agentSvc 由调用方注入(带预设 events)。
func setupReportHandlerTest(t *testing.T, agentSvc AgentService) (*AgentTaskHandler, repoagent.AgentTaskRepository, *mockMessageService) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &conversation.AgentStep{}, &conversation.Message{}))

	repo := repoagent.NewAgentTaskRepository(db)
	stepRepo := chatrepo.NewAgentStepRepository(db)
	registry := agentpkg.NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "查阅资料",
		PromptTemplate: "p", MaxSteps: 10, Timeout: 5000000000,
	}))
	svc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, stepRepo, nil, nil, nil)
	msgSvc := &mockMessageService{}
	handler := NewAgentTaskHandler(svc, agentSvc, registry, &mockLLMConfigService{hasConfig: false}, msgSvc)
	return handler, repo, msgSvc
}

// TestHandleReportTask_PersistsReportMessage 汇报应落库为 kind=report 消息:
// HandleReportTask 流式汇报后必须调 messageService.SaveReportMessage(content=最终汇报文本, task_id)。
// 修复前 handler 不落库 -> reportSaved=false(红)。
func TestHandleReportTask_PersistsReportMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	agentSvc := &mockAgentService{events: []agentpkg.AgentEvent{
		{Type: agentpkg.AgentEventFinal, Content: "调研结果:Go 1.24 支持泛型..."},
		{Type: agentpkg.AgentEventDone},
	}}
	handler, repo, msgSvc := setupReportHandlerTest(t, agentSvc)

	t1 := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("研究 Go 1.24"), "web", "")
	require.NoError(t, repo.Create(t1))
	art := "old artifact"
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, &art, nil))

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)
	req, _ := http.NewRequest("POST", "/api/v1/agent/tasks/"+strconv.FormatInt(t1.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code, "SSE 端点应 200")
	assert.True(t, msgSvc.reportSaved, "HandleReportTask 应调 SaveReportMessage 落库汇报")
	assert.Equal(t, t1.ID, msgSvc.savedReportTaskID, "汇报消息应关联 task_id")
	assert.Contains(t, msgSvc.savedReportContent, "调研结果:Go 1.24", "汇报内容应是主 Agent 最终文本")
}
