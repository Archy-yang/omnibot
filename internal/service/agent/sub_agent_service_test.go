package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"omnibot/internal/domain/conversation"
	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// mockRunner 可控的 SubAgentRunner mock:按 preset 返回结果或错误,记录调用。
type mockRunner struct {
	mu        sync.Mutex
	artifact  string
	err       error
	calls     []call
	delay     time.Duration // 模拟耗时(让 StartTask 的 goroutine 有时间跑)
}
type call struct {
	card domainagent.SubAgentCard
	goal string
}

func (m *mockRunner) Run(ctx context.Context, _ int64, card domainagent.SubAgentCard, goal string, onStep func(StepRecord)) (string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call{card: card, goal: goal})
	m.mu.Unlock()
	if m.delay > 0 {
		select {
		case <-time.After(m.delay):
		case <-ctx.Done():
		}
	}
	if m.err != nil {
		return "", m.err
	}
	if onStep != nil {
		onStep(StepRecord{Kind: StepKindLLMCall, Status: StepStatusSuccess})
	}
	return m.artifact, nil
}

func (m *mockRunner) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func setupSubAgentServiceTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	// sqlite :memory: 每个连接独立库;SubAgentService 的 executeTask 在后台 goroutine 跑,
	// 与主 goroutine 并发访问。强制单连接共享内存库,否则跨 goroutine 看不到数据。
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &conversation.AgentStep{}))
	return db
}

func setupSubAgentService(t *testing.T, runner SubAgentRunner) (*SubAgentService, *repoagent.GormAgentTaskRepository, chatrepo.AgentStepRepository) {
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db).(*repoagent.GormAgentTaskRepository)
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type:           "researcher",
		Name:           "研究员",
		Description:    "查阅资料",
		PromptTemplate: "你是研究员。目标:{goal}",
		Tools:          []string{"rss_reader"},
		MaxSteps:       15,
		Timeout:        5 * time.Second,
	}))
	stepRepo := chatrepo.NewAgentStepRepository(db)
	svc := NewSubAgentService(repo, registry, runner, stepRepo)
	return svc, repo, stepRepo
}

// waitForTaskStatus 轮询等任务达到目标状态(或超时),让 goroutine 有时间跑完。
func waitForTaskStatus(t *testing.T, repo repoagent.AgentTaskRepository, taskID int64, status string, timeout time.Duration) *domainagent.AgentTask {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		task, err := repo.GetByID(taskID)
		require.NoError(t, err)
		if task.Status == status {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _ := repo.GetByID(taskID)
	t.Fatalf("task %d did not reach status %s, got %s", taskID, status, task.Status)
	return nil
}

func TestSubAgentService_StartTask_Success(t *testing.T) {
	runner := &mockRunner{artifact: "Go 1.24 要点:① 泛型 ② ...", delay: 20 * time.Millisecond}
	svc, repo, stepRepo := setupSubAgentService(t, runner)

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", "研究 Go 1.24")
	require.NoError(t, err)
	assert.NotZero(t, taskID)

	// 立即返回时任务应是 pending 或 running(异步,不等)
	// 等 goroutine 跑完 -> completed
	task := waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusCompleted, 2*time.Second)
	assert.NotNil(t, task.Artifact)
	assert.Equal(t, "Go 1.24 要点:① 泛型 ② ...", *task.Artifact)
	assert.False(t, task.Reported)
	assert.Equal(t, 1, runner.callCount())
	assert.Equal(t, "研究 Go 1.24", runner.calls[0].goal)

	// 方案A:子 Agent 步骤应落 agent_steps,按 task_id 关联,MessageID 为 nil
	// mockRunner 调 onStep 1 次 -> 实时落 1 步
	steps, err := stepRepo.ListByTaskID(taskID)
	require.NoError(t, err)
	require.Len(t, steps, 1, "mockRunner onStep 调 1 次,应落 1 步")
	assert.Nil(t, steps[0].MessageID, "子 Agent 步骤 MessageID 应为 nil")
	require.NotNil(t, steps[0].TaskID)
	assert.Equal(t, taskID, *steps[0].TaskID)
}

func TestSubAgentService_StartTask_RunnerError(t *testing.T) {
	runner := &mockRunner{err: errors.New("llm timeout"), delay: 20 * time.Millisecond}
	svc, repo, _ := setupSubAgentService(t, runner)

	taskID, err := svc.StartTask(context.Background(), 1, "researcher", "goal")
	require.NoError(t, err)

	task := waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusFailed, 2*time.Second)
	assert.Nil(t, task.Artifact)
	require.NotNil(t, task.ErrorMsg)
	assert.Equal(t, "子 Agent 执行超时", *task.ErrorMsg, "错误应脱敏为友好文案")
}

func TestSubAgentService_StartTask_EmptyArtifact(t *testing.T) {
	runner := &mockRunner{artifact: "   ", delay: 20 * time.Millisecond} // 空白产出
	svc, repo, _ := setupSubAgentService(t, runner)

	taskID, _ := svc.StartTask(context.Background(), 1, "researcher", "goal")
	task := waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusFailed, 2*time.Second)
	require.NotNil(t, task.ErrorMsg)
	assert.Contains(t, *task.ErrorMsg, "未产出有效结果")
}

// incrementalMockRunner 先 onStep 产一步,再阻塞等 proceed 信号,然后返回 artifact。
// 模拟"子 Agent 正在跑"的中间态:步骤已产出但任务未结束,用于验证步骤实时落库。
type incrementalMockRunner struct {
	onStepRecord StepRecord
	proceed      chan struct{}
	artifact     string
}

func (m *incrementalMockRunner) Run(ctx context.Context, _ int64, _ domainagent.SubAgentCard, _ string, onStep func(StepRecord)) (string, error) {
	if onStep != nil {
		onStep(m.onStepRecord)
	}
	select {
	case <-m.proceed:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return m.artifact, nil
}

// TestSubAgentService_StepsSavedIncrementally 步骤必须边跑边落库,而非等任务结束批量落。
// runner 先 onStep 产一步再阻塞,此时任务仍 running,测试从 DB 能查到该步 -> 证明实时持久化。
// 回归保护:之前 runner 用聚合 Run(records 结束才返回)+ executeTask 末尾批量落,
// 任务跑的过程中 agent_steps 查不到任何 task_id 步骤。
func TestSubAgentService_StepsSavedIncrementally(t *testing.T) {
	proceed := make(chan struct{})
	runner := &incrementalMockRunner{
		onStepRecord: StepRecord{Kind: StepKindToolCall, Tool: "rss_reader", Status: StepStatusSuccess},
		proceed:      proceed,
		artifact:     "result",
	}
	svc, repo, stepRepo := setupSubAgentService(t, runner)

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", "g")
	require.NoError(t, err)

	// 任务 running 中就应能从 DB 查到 onStep 实时落的步骤(不等结束)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s, _ := stepRepo.ListByTaskID(taskID); len(s) >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	steps, err := stepRepo.ListByTaskID(taskID)
	require.NoError(t, err)
	require.Len(t, steps, 1, "任务 running 中就应能查到 onStep 实时落的步骤")
	require.NotNil(t, steps[0].TaskID)
	assert.Equal(t, taskID, *steps[0].TaskID)
	assert.Equal(t, "rss_reader", steps[0].Tool)
	assert.Nil(t, steps[0].MessageID, "子 Agent 步骤 MessageID 应为 nil")

	// 步骤已落但 runner 仍阻塞,任务应仍是 running
	task, err := repo.GetByID(taskID)
	require.NoError(t, err)
	assert.Equal(t, domainagent.TaskStatusRunning, task.Status, "步骤已落但 runner 仍阻塞,任务应仍 running")

	// 放行 runner -> 任务完成
	close(proceed)
	completed := waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusCompleted, 2*time.Second)
	assert.NotNil(t, completed.Artifact)
}

func TestSubAgentService_StartTask_UnregisteredType(t *testing.T) {
	svc, _, _ := setupSubAgentService(t, &mockRunner{})

	_, err := svc.StartTask(context.Background(), 1, "nonexistent", "goal")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestSubAgentService_GetCompletedUnreported(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{artifact: "result"})

	// 直接造任务(不经 StartTask,避免异步)
	t1 := domainagent.NewAgentTask(1, "researcher", "g1")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, strPtrService("a1"), nil))

	t2 := domainagent.NewAgentTask(1, "researcher", "g2")
	require.NoError(t, repo.Create(t2))
	require.NoError(t, repo.UpdateStatus(t2.ID, domainagent.TaskStatusCompleted, strPtrService("a2"), nil))
	require.NoError(t, repo.MarkReported(t2.ID)) // 已汇报

	// 别的用户的任务
	t3 := domainagent.NewAgentTask(2, "researcher", "g3")
	require.NoError(t, repo.Create(t3))
	require.NoError(t, repo.UpdateStatus(t3.ID, domainagent.TaskStatusCompleted, strPtrService("a3"), nil))

	got, err := svc.GetCompletedUnreported(1)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, t1.ID, got[0].ID)
}

func TestSubAgentService_MarkReported(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", "g")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, strPtrService("a"), nil))

	require.NoError(t, svc.MarkReported(task.ID))
	got, _ := svc.GetTask(task.ID)
	assert.True(t, got.Reported)
}

func strPtrService(s string) *string { return &s }
