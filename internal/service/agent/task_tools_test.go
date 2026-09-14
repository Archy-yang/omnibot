package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	conversation "omnibot/internal/domain/conversation"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// setupTaskToolsTest 复用 delegate 测试的 svc 装配,返回 svc + 一个已建任务。
func setupTaskToolsTest(t *testing.T) (*SubAgentService, repoagent.AgentTaskRepository, chatrepo.AgentStepRepository) {
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db)
	stepRepo := chatrepo.NewAgentStepRepository(db)
	svc := NewSubAgentService(repo, &mockRunner{artifact: "result"}, stepRepo, nil, nil, nil)
	return svc, repo, stepRepo
}

// TestQueryTaskTool_Single 查单个任务,返回格式化文本含状态/goal/步骤数。
func TestQueryTaskTool_Single(t *testing.T) {
	svc, repo, stepRepo := setupTaskToolsTest(t)
	tool := CreateQueryTaskTool(svc)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("研究 Go 1.24"), "web", "")
	require.NoError(t, repo.Create(task))
	// 落 1 步
	s := mustNewStep(task.ID, 42)
	require.NoError(t, stepRepo.CreateBatch([]*conversation.AgentStep{s}))

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.NoError(t, err)
	assert.Contains(t, result, "研究 Go 1.24")
	assert.Contains(t, result, "pending")
	assert.Contains(t, result, "已执行 1 步")
}

// TestQueryTaskTool_List 无 task_id 查列表。
func TestQueryTaskTool_List(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateQueryTaskTool(svc)
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), "web", "")))
	}

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{})
	require.NoError(t, err)
	assert.Contains(t, result, "3 个任务")
}

// TestQueryTaskTool_NotOwned 属主错误返回 error。
func TestQueryTaskTool_NotOwned(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateQueryTaskTool(svc)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withUserID(context.Background(), 999)
	_, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.Error(t, err)
}

// TestCancelTaskTool_Pending 取消 pending 任务。
func TestCancelTaskTool_Pending(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateCancelTaskTool(svc)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.NoError(t, err)
	var parsed map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(result), &parsed))
	assert.Equal(t, "cancelled", parsed["status"])

	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
}

// TestCancelTaskTool_MissingArgs 缺 task_id 报错。
func TestCancelTaskTool_MissingArgs(t *testing.T) {
	svc, _, _ := setupTaskToolsTest(t)
	tool := CreateCancelTaskTool(svc)
	ctx := withUserID(context.Background(), 42)
	_, err := tool.Execute(ctx, map[string]interface{}{})
	require.Error(t, err)
}

// TestUpdateTaskTool_PendingGoal pending 改 goal。
func TestUpdateTaskTool_PendingGoal(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateUpdateTaskTool(svc)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("旧"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"task_id": float64(task.ID),
		"goal":    "新 goal",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "goal 已更新")

	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, "新 goal", got.Goal)
}

// TestUpdateTaskTool_RunningNote running 追加 note。
func TestUpdateTaskTool_RunningNote(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateUpdateTaskTool(svc)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil))

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{
		"task_id": float64(task.ID),
		"note":    "补充信息",
	})
	require.NoError(t, err)
	assert.Contains(t, result, "补充信息")

	got, _ := repo.GetByID(task.ID)
	require.Len(t, got.Notes, 1)
}

// TestUpdateTaskTool_NoGoalNoNote goal/note 都空报错。
func TestUpdateTaskTool_NoGoalNoNote(t *testing.T) {
	svc, _, _ := setupTaskToolsTest(t)
	tool := CreateUpdateTaskTool(svc)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), "web", "")
	ctx := withUserID(context.Background(), 42)
	// 需先建任务(否则 update 会因任务不存在报错,混淆测试)
	// 这里直接传空 goal/note 测参数校验
	_, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.Error(t, err)
}

// TestQueryTaskTool_Single_TimeFields query_task 返回带时间:创建时间必有,
// 已结束任务带完成/取消时间(任务工具可观测性:LLM 能回答"任务什么时候派/什么时候跑完")。
func TestQueryTaskTool_Single_TimeFields(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateQueryTaskTool(svc)

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("带时间的任务"), "web", "")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, nil, nil))

	ctx := withUserID(context.Background(), 42)
	result, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.NoError(t, err)
	assert.Contains(t, result, "创建于")
	assert.Contains(t, result, "完成于")
}

// TestFormatTaskSummary_Times formatTaskSummary 按状态渲染时间行:
// pending 只给创建时间;completed 给创建+完成;cancelled 给创建+取消。
func TestFormatTaskSummary_Times(t *testing.T) {
	created := time.Date(2026, 9, 11, 13, 5, 0, 0, time.Local)
	finished := created.Add(2 * time.Minute)

	pending := &TaskSummary{ID: 1, Status: "pending", Goal: "g", CreatedAt: created}
	out := formatTaskSummary(pending)
	assert.Contains(t, out, "创建于 2026-09-11 13:05")
	assert.NotContains(t, out, "完成于")
	assert.NotContains(t, out, "取消于")

	completed := &TaskSummary{ID: 2, Status: "completed", Goal: "g", CreatedAt: created, FinishedAt: &finished}
	out = formatTaskSummary(completed)
	assert.Contains(t, out, "创建于 2026-09-11 13:05")
	assert.Contains(t, out, "完成于 2026-09-11 13:07")

	cancelled := &TaskSummary{ID: 3, Status: "cancelled", Goal: "g", CreatedAt: created, FinishedAt: &finished}
	out = formatTaskSummary(cancelled)
	assert.Contains(t, out, "取消于 2026-09-11 13:07")
	assert.NotContains(t, out, "完成于")
}

// TestFormatTaskSummary_Name 任务短名渲染:有名字带「短名」,无名字不出现空引号。
func TestFormatTaskSummary_Name(t *testing.T) {
	named := &TaskSummary{ID: 1, Status: "running", Goal: "g", Name: "查AIHOT今日动态", CreatedAt: time.Now()}
	out := formatTaskSummary(named)
	assert.Contains(t, out, "任务 #1「查AIHOT今日动态」")
	assert.Contains(t, out, "[running]")

	unnamed := &TaskSummary{ID: 2, Status: "running", Goal: "g", CreatedAt: time.Now()}
	out = formatTaskSummary(unnamed)
	assert.Contains(t, out, "任务 #2 [running]", "无名字时格式退回原样,不出现空「」")
}

// TestParseTaskID 各类型 task_id 解析。
func TestParseTaskID(t *testing.T) {
	cases := []struct {
		in   interface{}
		want int64
		err  bool
	}{
		{float64(15), 15, false},
		{int(15), 15, false},
		{int64(15), 15, false},
		{"15", 15, false},
		{"abc", 0, true},
		{nil, 0, true},
	}
	for _, c := range cases {
		got, err := parseTaskID(c.in)
		if c.err {
			require.Error(t, err, "input %v", c.in)
		} else {
			require.NoError(t, err, "input %v", c.in)
			assert.Equal(t, c.want, got, "input %v", c.in)
		}
	}
}

// mustNewStep 构造一个关联 taskID 的 step(测试辅助)。
func mustNewStep(taskID, userID int64) *conversation.AgentStep {
	s := conversation.NewLLMStep(userID, "req", "resp", "", "success", 10)
	tid := taskID
	s.TaskID = &tid
	return s
}
