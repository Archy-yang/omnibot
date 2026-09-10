package web

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"omnibot/internal/client/llm"
	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
	agentpkg "omnibot/internal/service/agent"
)

// 汇报锚定测试(§3.4 修订):
// HandleReportTask 的 report conversation 应包含最近对话历史(BuildContextMessages),
// 使主 Agent 能锚定"用户当初为什么派这个活",而非只对着回执凭空转述。
func TestHandleReportTask_IncludeConversationHistory(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}))
	repo := repoagent.NewAgentTaskRepository(db)
	subSvc := agentpkg.NewSubAgentService(repo, &webMockRunner{}, chatrepo.NewAgentStepRepository(db), nil, nil, nil)

	// completed 未汇报任务,带完成标准(验收自查用)
	spec := domainagent.NewTaskSpec("研究 Go 1.24 新特性")
	spec.CompletionCriteria = []string{"覆盖语言与标准库变化"}
	task := domainagent.NewAgentTask(42, spec, "web", "")
	require.NoError(t, repo.Create(task))
	art := "Go 1.24 要点全文"
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, &art, nil))

	agentSvc := &mockAgentService{events: []agentpkg.AgentEvent{
		{Type: agentpkg.AgentEventFinal, Content: "汇报完毕"},
		{Type: agentpkg.AgentEventDone, Content: "汇报完毕"},
	}}
	// 模拟历史:用户当初的原始问题在对话里
	msgSvc := &mockMessageService{}
	msgSvc.ctxMessages = []llm.ChatMessage{
		{Role: "user", Content: "帮我研究下 Go 1.24 的新特性"},
		{Role: "assistant", Content: "好的,我安排后台任务去查,完成后向你汇报。"},
	}
	handler := NewAgentTaskHandler(subSvc, agentSvc, &mockLLMConfigService{hasConfig: false}, msgSvc)

	router := gin.New()
	router.Use(injectUserID(42))
	router.POST("/api/v1/agent/tasks/:id/report", handler.HandleReportTask)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/agent/tasks/"+strconv.FormatInt(task.ID, 10)+"/report", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	conv := agentSvc.capturedStreamConversation
	require.NotEmpty(t, conv)

	// 首条:汇报指令 system(含验收自查要求)
	first := conv[0]
	assert.Equal(t, "system", first["role"])
	instruction, _ := first["content"].(string)
	assert.Contains(t, instruction, "子任务完成回执")
	assert.Contains(t, instruction, "完成标准")
	assert.Contains(t, instruction, "如实")

	// 原始问题经对话历史进入汇报上下文(锚定"当初为什么派这个活")
	var historyJoined bool
	for _, m := range conv[1:] {
		if c, _ := m["content"].(string); strings.Contains(c, "帮我研究下 Go 1.24") {
			historyJoined = true
		}
	}
	assert.True(t, historyJoined, "对话历史(原始问题)应进入 report conversation:\n%v", conv)

	// 末条:BuildContextMessages 产出的虚拟触发 user 消息
	last := conv[len(conv)-1]
	assert.Equal(t, "user", last["role"])
	lastContent, _ := last["content"].(string)
	assert.Contains(t, lastContent, "请汇报这个子任务的结果")
}
