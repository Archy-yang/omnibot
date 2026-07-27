package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

// TestHandleSendMessageAgentStream_PreReportInjectsReceipt 验证前置汇报兜底:
// 有未汇报任务时,主 Agent RunStream 收到的 conversation 首条是回执 system 消息。
func TestHandleSendMessageAgentStream_PreReportInjectsReceipt(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 建带任务的 SubAgentService
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
		PromptTemplate: "p", Tools: []string{}, MaxSteps: 10, Timeout: 5 * time.Second,
	}))
	subSvc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, chatrepo.NewAgentStepRepository(db))

	// 造一个 completed 未汇报任务
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("研究 Go 1.24"))
	require.NoError(t, repo.Create(task))
	art := "Go 1.24 要点"
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, &art, nil))

	// 主 Handler,注入 SubAgent 支持
	agentSvc := &mockAgentService{events: []agentpkg.AgentEvent{
		{Type: agentpkg.AgentEventFinal, Content: "汇报完毕"},
		{Type: agentpkg.AgentEventDone, Content: "汇报完毕"},
	}}
	handler := NewHandler(
		&mockUserService{userID: 42, created: false},
		&mockMessageService{},
		&mockLLMClient{},
		&mockLLMConfigService{hasConfig: false},
		&mockMemoryService{},
		agentSvc,
	)
	handler.SetSubAgentSupport(subSvc, registry)

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "你好"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// 验证 RunStream 收到的 conversation 首条是回执 system 消息
	require.NotEmpty(t, agentSvc.capturedStreamConversation)
	first := agentSvc.capturedStreamConversation[0]
	assert.Equal(t, "system", first["role"])
	content, _ := first["content"].(string)
	assert.Contains(t, content, "子任务完成回执")
	assert.Contains(t, content, "研究 Go 1.24")
	assert.Contains(t, content, "Go 1.24 要点")

	// 验证汇报后任务标记 reported
	got, _ := repo.GetByID(task.ID)
	assert.True(t, got.Reported, "前置汇报后应标记 reported")
}

// TestHandleSendMessageAgentStream_NoPreReportWhenNoTasks 验证无未汇报任务时
// 不注入回执(正常对话)。
func TestHandleSendMessageAgentStream_NoPreReportWhenNoTasks(t *testing.T) {
	gin.SetMode(gin.TestMode)

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
		Type: "researcher", Name: "研究员", Description: "d",
		PromptTemplate: "p", Tools: []string{}, MaxSteps: 10, Timeout: 5 * time.Second,
	}))
	subSvc := agentpkg.NewSubAgentService(repo, registry, &webMockRunner{}, chatrepo.NewAgentStepRepository(db))
	// 不造任务

	agentSvc := &mockAgentService{events: []agentpkg.AgentEvent{
		{Type: agentpkg.AgentEventFinal, Content: "你好"},
		{Type: agentpkg.AgentEventDone, Content: "你好"},
	}}
	handler := NewHandler(
		&mockUserService{userID: 42, created: false},
		&mockMessageService{},
		&mockLLMClient{},
		&mockLLMConfigService{hasConfig: false},
		&mockMemoryService{},
		agentSvc,
	)
	handler.SetSubAgentSupport(subSvc, registry)

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/chat/messages/agent/stream", handler.HandleSendMessageAgentStream)

	body, _ := json.Marshal(map[string]string{"content": "你好"})
	req, _ := http.NewRequest("POST", "/api/v1/chat/messages/agent/stream", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// 无未汇报任务:conversation 首条不应是回执 system(mockMessageService.BuildContextMessages
	// 返回的上下文里没有回执)
	require.NotEmpty(t, agentSvc.capturedStreamConversation)
	for _, msg := range agentSvc.capturedStreamConversation {
		content, _ := msg["content"].(string)
		assert.NotContains(t, content, "子任务完成回执")
	}
	_ = context.Background()
}
