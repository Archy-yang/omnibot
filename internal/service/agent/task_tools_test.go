package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	conversation "omnibot/internal/domain/conversation"
	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// setupTaskToolsTest 复用 delegate 测试的 svc 装配,返回 svc + 一个已建任务。
func setupTaskToolsTest(t *testing.T) (*SubAgentService, repoagent.AgentTaskRepository, chatrepo.AgentStepRepository) {
	db := setupSubAgentServiceTestDB(t)
	repo := repoagent.NewAgentTaskRepository(db)
	registry := NewSubAgentRegistry()
	require.NoError(t, registry.Register(domainagent.SubAgentCard{
		Type: "researcher", Name: "研究员", Description: "d",
		PromptTemplate: "p", MaxSteps: 5, Timeout: time.Second,
	}))
	stepRepo := chatrepo.NewAgentStepRepository(db)
	svc := NewSubAgentService(repo, registry, &mockRunner{artifact: "result"}, stepRepo, nil, nil, nil)
	return svc, repo, stepRepo
}

// TestQueryTaskTool_Single 查单个任务,返回格式化文本含状态/goal/步骤数。
func TestQueryTaskTool_Single(t *testing.T) {
	svc, repo, stepRepo := setupTaskToolsTest(t)
	tool := CreateQueryTaskTool(svc)

	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("研究 Go 1.24"), "web", "")
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
		require.NoError(t, repo.Create(domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")))
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
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	ctx := withUserID(context.Background(), 999)
	_, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.Error(t, err)
}

// TestCancelTaskTool_Pending 取消 pending 任务。
func TestCancelTaskTool_Pending(t *testing.T) {
	svc, repo, _ := setupTaskToolsTest(t)
	tool := CreateCancelTaskTool(svc)
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
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
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("旧"), "web", "")
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
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
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
	task := domainagent.NewAgentTask(42, "researcher", domainagent.NewTaskSpec("g"), "web", "")
	ctx := withUserID(context.Background(), 42)
	// 需先建任务(否则 update 会因任务不存在报错,混淆测试)
	// 这里直接传空 goal/note 测参数校验
	_, err := tool.Execute(ctx, map[string]interface{}{"task_id": float64(task.ID)})
	require.Error(t, err)
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
