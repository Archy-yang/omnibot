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

func (m *mockRunner) Run(ctx context.Context, _ int64, _ int64, card domainagent.SubAgentCard, spec domainagent.TaskSpec, onStep func(StepRecord)) (string, error) {
	m.mu.Lock()
	m.calls = append(m.calls, call{card: card, goal: spec.Goal})
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
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &conversation.AgentStep{}, &domainagent.Artifact{}, &domainagent.TaskEvent{}))
	return db
}

func setupSubAgentService(t *testing.T, runner SubAgentRunner) (*SubAgentService, *repoagent.GormAgentTaskRepository, chatrepo.AgentStepRepository) {
	return setupSubAgentServiceWithArtifact(t, runner, false)
}

// setupSubAgentServiceWithArtifact 可选是否装配 artifactRepo(测 artifact 落库时 withArtifact=true)。
func setupSubAgentServiceWithArtifact(t *testing.T, runner SubAgentRunner, withArtifact bool) (*SubAgentService, *repoagent.GormAgentTaskRepository, chatrepo.AgentStepRepository) {
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db).(*repoagent.GormAgentTaskRepository)
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type:           "researcher",
		Name:           "研究员",
		Description:    "查阅资料",
		PromptTemplate: "你是研究员。目标:{goal}",
		MaxSteps:       15,
		Timeout:        5 * time.Second,
	}))
	stepRepo := chatrepo.NewAgentStepRepository(db)
	var artifactRepo repoagent.ArtifactRepository
	if withArtifact {
		artifactRepo = repoagent.NewArtifactRepository(db)
	}
	// eventRepo 始终建(事件落库测试用)
	eventRepo := repoagent.NewTaskEventRepository(db)
	svc := NewSubAgentService(repo, registry, runner, stepRepo, artifactRepo, eventRepo, nil)
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

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", domainagent.NewTaskSpec("研究 Go 1.24"), "web", "")
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

// TestSubAgentService_ArtifactPersisted 任务完成时应落结构化 artifact(独立表)。
// 兼容:task.Artifact 文本仍存。artifactRepo 装配时双写。
func TestSubAgentService_ArtifactPersisted(t *testing.T) {
	runner := &mockRunner{artifact: "# 报告\n内容", delay: 20 * time.Millisecond}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, runner, true)

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, err)
	waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusCompleted, 2*time.Second)

	// 查 artifact 表
	art, err := svc.artifactRepo.GetByTaskID(taskID)
	require.NoError(t, err)
	assert.Equal(t, taskID, art.TaskID)
	assert.Equal(t, domainagent.ArtifactContentTypeMarkdown, art.ContentType)
	assert.Equal(t, "# 报告\n内容", art.Text())
	// task.Artifact 文本仍存(向后兼容)
	task, _ := repo.GetByID(taskID)
	require.NotNil(t, task.Artifact)
	assert.Equal(t, "# 报告\n内容", *task.Artifact)
}

// TestSubAgentService_EventsRecorded 任务生命周期事件落 agent_task_events(#22)。
// 完整路径(submitted->running->completed)应各记一条事件,sequence 递增。
func TestSubAgentService_EventsRecorded(t *testing.T) {
	runner := &mockRunner{artifact: "结果", delay: 20 * time.Millisecond}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, runner, true)

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, err)
	waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusCompleted, 2*time.Second)

	events, err := svc.eventRepo.ListByTaskID(taskID)
	require.NoError(t, err)
	// submitted(main) -> running(sub) -> completed(sub)
	require.Len(t, events, 3)
	assert.Equal(t, domainagent.EventTaskSubmitted, events[0].EventType)
	assert.Equal(t, "main", events[0].SourceAgent)
	assert.Equal(t, 1, events[0].Sequence)
	assert.Equal(t, domainagent.EventTaskRunning, events[1].EventType)
	assert.Equal(t, 2, events[1].Sequence)
	assert.Equal(t, domainagent.EventTaskCompleted, events[2].EventType)
	assert.Equal(t, 3, events[2].Sequence)
}

func TestSubAgentService_StartTask_RunnerError(t *testing.T) {
	runner := &mockRunner{err: errors.New("llm timeout"), delay: 20 * time.Millisecond}
	svc, repo, _ := setupSubAgentService(t, runner)

	taskID, err := svc.StartTask(context.Background(), 1, "researcher", domainagent.NewTaskSpec("goal"), "web", "")
	require.NoError(t, err)

	task := waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusFailed, 2*time.Second)
	assert.Nil(t, task.Artifact)
	require.NotNil(t, task.ErrorMsg)
	assert.Equal(t, "子 Agent 执行超时", *task.ErrorMsg, "错误应脱敏为友好文案")
}

func TestSubAgentService_StartTask_EmptyArtifact(t *testing.T) {
	runner := &mockRunner{artifact: "   ", delay: 20 * time.Millisecond} // 空白产出
	svc, repo, _ := setupSubAgentService(t, runner)

	taskID, _ := svc.StartTask(context.Background(), 1, "researcher", domainagent.NewTaskSpec("goal"), "web", "")
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

func (m *incrementalMockRunner) Run(ctx context.Context, _ int64, _ int64, _ domainagent.SubAgentCard, _ domainagent.TaskSpec, onStep func(StepRecord)) (string, error) {
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

	taskID, err := svc.StartTask(context.Background(), 42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
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

	_, err := svc.StartTask(context.Background(), 1, "nonexistent", domainagent.NewTaskSpec("goal"), "web", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not registered")
}

func TestSubAgentService_GetCompletedUnreported(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{artifact: "result"})

	// 直接造任务(不经 StartTask,避免异步)
	t1 := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g1"), "web", "")
	require.NoError(t, repo.Create(t1))
	require.NoError(t, repo.UpdateStatus(t1.ID, domainagent.TaskStatusCompleted, strPtrService("a1"), nil))

	t2 := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g2"), "web", "")
	require.NoError(t, repo.Create(t2))
	require.NoError(t, repo.UpdateStatus(t2.ID, domainagent.TaskStatusCompleted, strPtrService("a2"), nil))
	require.NoError(t, repo.MarkReported(t2.ID)) // 已汇报

	// 别的用户的任务
	t3 := domainagent.NewAgentTask(2, "researcher", domainagent.NewTaskSpec("g3"), "web", "")
	require.NoError(t, repo.Create(t3))
	require.NoError(t, repo.UpdateStatus(t3.ID, domainagent.TaskStatusCompleted, strPtrService("a3"), nil))

	got, err := svc.GetCompletedUnreported(1)
	require.NoError(t, err)
	assert.Len(t, got, 1)
	assert.Equal(t, t1.ID, got[0].ID)
}

func TestSubAgentService_MarkReported(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, strPtrService("a"), nil))

	require.NoError(t, svc.MarkReported(task.ID))
	got, _ := svc.GetTask(task.ID)
	assert.True(t, got.Reported)
}

// TestSubAgentService_QueryTask 查单个任务概要(状态+goal+步骤数)。属主校验。
func TestSubAgentService_QueryTask(t *testing.T) {
	svc, repo, stepRepo := setupSubAgentService(t, &mockRunner{artifact: "result"})

	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("研究 Go 1.24"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))
	// 落 2 步(关联 task_id)
	s1 := conversation.NewLLMStep(1, "req", "resp", "", "success", 10)
	s2 := conversation.NewToolStep(1, "rss_reader", "{}", "ok", "success", 5)
	tid := task.ID
	s1.TaskID = &tid
	s2.TaskID = &tid
	require.NoError(t, stepRepo.CreateBatch([]*conversation.AgentStep{s1, s2}))

	// 属主正确
	summary, err := svc.QueryTask(1, task.ID)
	require.NoError(t, err)
	assert.Equal(t, task.ID, summary.ID)
	assert.Equal(t, domainagent.TaskStatusRunning, summary.Status)
	assert.Equal(t, "研究 Go 1.24", summary.Goal)
	assert.Equal(t, 2, summary.StepCount)

	// 属主错误
	_, err = svc.QueryTask(999, task.ID)
	assert.ErrorIs(t, err, ErrTaskNotOwned)
}

// TestSubAgentService_ListUserTasks 列出用户任务(倒序),不含其他用户。
func TestSubAgentService_ListUserTasks(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	for i := 0; i < 3; i++ {
		tk := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
		require.NoError(t, repo.Create(tk))
	}
	tk2 := domainagent.NewAgentTask(2, "researcher", domainagent.NewTaskSpec("other"), "web", "")
	require.NoError(t, repo.Create(tk2))

	list, err := svc.ListUserTasks(1, 10)
	require.NoError(t, err)
	assert.Len(t, list, 3)
	for _, s := range list {
		assert.Equal(t, int64(1), s.UserID)
	}
}

// TestSubAgentService_CancelTask_Pending pending 任务直接取消(没起 runner)。
func TestSubAgentService_CancelTask_Pending(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task)) // pending

	require.NoError(t, svc.CancelTask(1, task.ID))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
}

// TestSubAgentService_CancelTask_TerminalRejected 已结束任务不可取消。
func TestSubAgentService_CancelTask_TerminalRejected(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, strPtrService("a"), nil))

	err := svc.CancelTask(1, task.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已结束")
}

// TestSubAgentService_CancelTask_RunningTriggersCtxCancel running 任务取消应触发 ctx 取消。
func TestSubAgentService_CancelTask_RunningTriggersCtxCancel(t *testing.T) {
	proceed := make(chan struct{})
	runner := &ctxCaptureRunner{proceed: proceed, artifact: "result"}
	svc, repo, _ := setupSubAgentService(t, runner)

	taskID, err := svc.StartTask(context.Background(), 1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, err)
	// 等 runner 启动(running 状态 + cancel 注册)
	waitForTaskStatus(t, repo, taskID, domainagent.TaskStatusRunning, 2*time.Second)

	// 取消 running 任务
	require.NoError(t, svc.CancelTask(1, taskID))
	// 放行 runner(它应因 ctx 取消已返回,这里 close 防止卡住)
	close(proceed)
	time.Sleep(50 * time.Millisecond)

	got, _ := repo.GetByID(taskID)
	// 取消后应为 cancelled(而非 failed)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
}

// TestSubAgentService_UpdateTask_Pending pending 改 goal。
func TestSubAgentService_UpdateTask_Pending(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("旧 goal"), "web", "")
	require.NoError(t, repo.Create(task))

	require.NoError(t, svc.UpdateTask(1, task.ID, "新 goal", ""))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, "新 goal", got.Goal)
	assert.Empty(t, got.Notes)
}

// TestSubAgentService_UpdateTask_RunningAppendNote running 追加 notes。
func TestSubAgentService_UpdateTask_RunningAppendNote(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{artifact: "result"})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	require.NoError(t, svc.UpdateTask(1, task.ID, "", "补充信息1"))
	got, _ := repo.GetByID(task.ID)
	require.Len(t, got.Notes, 1)
	assert.Equal(t, "补充信息1", got.Notes[0])
	// goal 不变(空 goal 不覆盖)
	assert.Equal(t, "g", got.Goal)
}

// TestSubAgentService_RequestInput 子 Agent 主动要输入:置 input_required + 问题存 Notes。
func TestSubAgentService_RequestInput(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	require.NoError(t, svc.RequestInput(task.ID, "你更关注自部署还是云服务?"))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusInputRequired, got.Status)
	require.Len(t, got.Notes, 1)
	assert.Contains(t, got.Notes[0], "需要输入")
	assert.Contains(t, got.Notes[0], "自部署还是云服务")
}

// TestSubAgentService_UpdateTask_InputRequired input_required 态可补 note(和 running 同)。
func TestSubAgentService_UpdateTask_InputRequired(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusInputRequired, nil, nil))

	// 补 note 不报错(input_required 非终态)
	require.NoError(t, svc.UpdateTask(1, task.ID, "", "自部署"))
	got, _ := repo.GetByID(task.ID)
	require.Len(t, got.Notes, 1)
	assert.Equal(t, "自部署", got.Notes[0])
}

// TestSubAgentService_CancelTask_InputRequired input_required 态可取消(非终态)。
func TestSubAgentService_CancelTask_InputRequired(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusInputRequired, nil, nil))

	require.NoError(t, svc.CancelTask(1, task.ID))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
}

// TestRequestInputTool 子 Agent 调 request_input 工具,任务置 input_required + 问题存 Notes。
func TestRequestInputTool(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	tool := CreateRequestInputTool(svc)
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withTaskID(context.Background(), task.ID)
	result, err := tool.Execute(ctx, map[string]interface{}{"question": "用 PostgreSQL 还是 MySQL?"})
	require.NoError(t, err)
	assert.Contains(t, result, "PostgreSQL 还是 MySQL")

	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusInputRequired, got.Status)
	require.Len(t, got.Notes, 1)
	assert.Contains(t, got.Notes[0], "PostgreSQL 还是 MySQL")
}

// TestRequestInputTool_NoTaskID 主 Agent(无 taskID)调 request_input 应报错。
func TestRequestInputTool_NoTaskID(t *testing.T) {
	svc, _, _ := setupSubAgentService(t, &mockRunner{})
	tool := CreateRequestInputTool(svc)
	_, err := tool.Execute(context.Background(), map[string]interface{}{"question": "x"})
	require.Error(t, err)
}

// TestSubAgentService_UpdateTask_TerminalRejected 已结束任务不可更新。
func TestSubAgentService_UpdateTask_TerminalRejected(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{})
	task := domainagent.NewAgentTask(1, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, strPtrService("err")))

	err := svc.UpdateTask(1, task.ID, "new", "note")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "已结束")
}

// ctxCaptureRunner 捕获 runner 收到的 ctx,供 cancel 测试断言 ctx 被取消。
type ctxCaptureRunner struct {
	proceed  chan struct{}
	artifact string
	gotCtx   context.Context
}

func (r *ctxCaptureRunner) Run(ctx context.Context, _ int64, _ int64, _ domainagent.SubAgentCard, _ domainagent.TaskSpec, _ func(StepRecord)) (string, error) {
	r.gotCtx = ctx
	select {
	case <-r.proceed:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return r.artifact, nil
}

// TaskSummary 任务概要(供 query_task 工具返回给 LLM)。定义在 service 层,测试这里复用。

func strPtrService(s string) *string { return &s }
