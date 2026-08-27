package agent

import (
	"context"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// 回归测试:验证「子 Agent 执行过程(LLM调用 + 工具调用)是否真的落 agent_steps」。
//
// 现有 sub_agent_service_test.go 只用 mockRunner(直接返回硬编码的 1 条 StepRecord),
// 没有覆盖真实的 subAgentRunnerImpl(它要靠 AgentService.Run drain RunStream 才能产出 records)。
// 本测试用真实 runner + mock 流式 LLM,覆盖成功/失败两条路径的落库情况。
//
// 跑法: go test ./internal/service/agent/ -run TestSubAgentSteps -v

func stepsDiagSetup(t *testing.T, llm StreamingLLMClient) (*SubAgentService, *repoagent.GormAgentTaskRepository, chatrepo.AgentStepRepository) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	sqlDB, _ := db.DB()
	// 子 Agent 在后台 goroutine 跑,必须强制单连接共享内存库
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &conversation.AgentStep{}))

	taskRepo := repoagent.NewAgentTaskRepository(db).(*repoagent.GormAgentTaskRepository)
	stepRepo := chatrepo.NewAgentStepRepository(db)

	// 全局工具池:注册一个假的 rss_reader(Execute 直接返 canned 结果,不走网络)
	globalReg := NewToolRegistry()
	require.NoError(t, globalReg.Register(Tool{
		Name:         "rss_reader",
		DisplayLabel: "读取了 RSS",
		Description:  "fake rss reader for diag",
		Capabilities: []string{CapResearch}, // 对齐生产打标:子 Agent 可见性走能力白名单
		Parameters:   map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return `{"title":"fake","items":[{"title":"文章A"}]}`, nil
		},
	}))

	// 真实 runner:用 mock 流式 LLM(noopSyncLLM 作 sync 占位,流式路径不会用到)
	runner := NewSubAgentRunner(noopSyncLLM{}, llm, globalReg, nil, 0, nil, nil)
	svc := NewSubAgentService(taskRepo, runner, stepRepo, nil, nil, nil)
	return svc, taskRepo, stepRepo
}

func stepsDiagWaitStatus(t *testing.T, repo repoagent.AgentTaskRepository, taskID int64, want string, timeout time.Duration) *domainagent.AgentTask {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk, err := repo.GetByID(taskID)
		require.NoError(t, err)
		if tk.Status == want {
			return tk
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := repo.GetByID(taskID)
	t.Fatalf("task %d did not reach %s, got %s", taskID, want, tk.Status)
	return nil
}

// 成功路径:子 Agent 调一次 rss_reader 再给最终报告。期望 agent_steps 落 3 条(2 llm_call + 1 tool_call)。
func TestSubAgentSteps_SuccessSavesSteps(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// round1: 思考 + 调 rss_reader
			{
				{ContentDelta: "我来查一下。"},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "rss_reader", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// round2: 最终报告(无工具)
			{
				{ContentDelta: "调研完成:文章A。"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	svc, taskRepo, stepRepo := stepsDiagSetup(t, llm)

	taskID, err := svc.StartTask(context.Background(), 42, domainagent.NewTaskSpec("研究某主题"), "web", "")
	require.NoError(t, err)

	task := stepsDiagWaitStatus(t, taskRepo, taskID, domainagent.TaskStatusCompleted, 3*time.Second)
	require.NotNil(t, task.Artifact)
	assert.Equal(t, "调研完成:文章A。", *task.Artifact)

	steps, err := stepRepo.ListByTaskID(taskID)
	require.NoError(t, err)
	t.Logf("成功路径:agent_steps 落了 %d 条", len(steps))
	for i, s := range steps {
		t.Logf("  step[%d] kind=%s tool=%s status=%s", i, s.Kind, s.Tool, s.Status)
	}

	// 期望:3 条(2 llm_call + 1 tool_call),全部 TaskID 关联,MessageID 为 nil
	require.Len(t, steps, 3, "成功路径应落 3 条步骤(2 llm_call + 1 tool_call)")
	assert.Nil(t, steps[0].MessageID)
	for _, s := range steps {
		require.NotNil(t, s.TaskID)
		assert.Equal(t, taskID, *s.TaskID)
	}
	// 顺序:llm_call(round1) -> tool_call -> llm_call(round2)
	assert.Equal(t, "llm_call", steps[0].Kind)
	assert.Equal(t, "tool_call", steps[1].Kind)
	assert.Equal(t, "rss_reader", steps[1].Tool)
	assert.Equal(t, "success", steps[1].Status)
	assert.Equal(t, "llm_call", steps[2].Kind)
}

// 失败路径:LLM 流打开就失败。RunStream 会 emit 一条 error 状态的 llm_call 事件,
// 期望:即便任务最终 failed,这条执行过程也要落 agent_steps 供复盘(见 service.go 修复)。
// 修复前 AgentService.Run 在 err 时返回 nil records,executeTask 拿到 nil 啥也不落--与
// sub_agent_service.go 注释「失败也落步骤」矛盾。本测试锁定修复后行为。
func TestSubAgentSteps_FailureStillSavesSteps(t *testing.T) {
	llm := &mockStreamingLLMClient{
		openErr: newSimpleErr("llm 503 unavailable"),
	}
	svc, taskRepo, stepRepo := stepsDiagSetup(t, llm)

	taskID, err := svc.StartTask(context.Background(), 42, domainagent.NewTaskSpec("研究某主题"), "web", "")
	require.NoError(t, err)

	task := stepsDiagWaitStatus(t, taskRepo, taskID, domainagent.TaskStatusFailed, 3*time.Second)
	require.NotNil(t, task.ErrorMsg)

	steps, err := stepRepo.ListByTaskID(taskID)
	require.NoError(t, err)
	t.Logf("失败路径:agent_steps 落了 %d 条(期望>=1:RunStream 打开失败已 emit 的 error llm_call)", len(steps))

	// 期望:失败任务至少落 1 条 error 状态的 llm_call(执行过程不丢)
	require.NotEmpty(t, steps, "失败任务也应落步骤供复盘 -- Run 不应在 err 时丢弃 records")
	var hasErrLLMCall bool
	for _, s := range steps {
		require.NotNil(t, s.TaskID)
		assert.Equal(t, taskID, *s.TaskID)
		if s.Kind == "llm_call" && s.Status == "error" {
			hasErrLLMCall = true
		}
	}
	assert.True(t, hasErrLLMCall, "应包含打开失败那条 error llm_call 步骤")
}

// newSimpleErr 一个简单 error 用于 openErr 模拟。
type simpleErr struct{ msg string }

func (e *simpleErr) Error() string  { return e.msg }
func newSimpleErr(msg string) error { return &simpleErr{msg: msg} }
