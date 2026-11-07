package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/pkg/toolcore"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
)

// 34 号 §3.4:delegate 带 steps 建计划树;manage_plan 查/取消。

func contextWithUser(userID int64) context.Context {
	return toolcore.WithUserID(context.Background(), userID)
}

func setupPlanToolTest(t *testing.T) (Tool, Tool, *fakePlanGateway, *PlanRunner) {
	t.Helper()
	store := newFakePlanStore()
	gw := newFakePlanGateway(store)
	runner := NewPlanRunner(store, gw)

	db := setupSubAgentServiceTestDB(t)
	taskRepo := repoagent.NewAgentTaskRepository(db)
	svc := NewSubAgentService(taskRepo, &mockRunner{artifact: "ok"}, chatrepo.NewAgentStepRepository(db), nil, nil, nil)

	return CreateDelegateTool(svc, runner), CreateManagePlanTool(runner), gw, runner
}

func TestDelegateTool_WithStepsCreatesPlan(t *testing.T) {
	delegate, _, gw, _ := setupPlanToolTest(t)

	out, err := delegate.Execute(contextWithUser(42), map[string]interface{}{
		"goal": "做一份云南 7 天行程",
		"name": "云南7日行",
		"steps": []interface{}{
			map[string]interface{}{"name": "查资料", "goal": "查目的地"},
			map[string]interface{}{"name": "排路线", "goal": "排路线"},
		},
	})
	require.NoError(t, err)
	assert.Contains(t, out, "plan_id")
	assert.Contains(t, out, "已拆成 2 步")
	require.Len(t, gw.launched, 1, "建计划后立即派第一步")
}

func TestDelegateTool_WithStepsRejectsSingleStep(t *testing.T) {
	delegate, _, gw, _ := setupPlanToolTest(t)
	_, err := delegate.Execute(contextWithUser(42), map[string]interface{}{
		"goal":  "g",
		"steps": []interface{}{map[string]interface{}{"name": "只有一步", "goal": "g"}},
	})
	require.Error(t, err, "一步等于单任务")
	assert.Empty(t, gw.launched)
}

func TestDelegateTool_WithoutStepsUnchanged(t *testing.T) {
	delegate, _, gw, _ := setupPlanToolTest(t)
	out, err := delegate.Execute(contextWithUser(42), map[string]interface{}{"goal": "查天气"})
	require.NoError(t, err)
	assert.Contains(t, out, "task_id")
	assert.NotContains(t, out, "plan_id")
	assert.Empty(t, gw.launched, "不带 steps 不走计划")
}

func TestManagePlan_StatusAndCancel(t *testing.T) {
	delegate, manage, gw, runner := setupPlanToolTest(t)
	_, err := delegate.Execute(contextWithUser(42), map[string]interface{}{
		"goal": "g",
		"steps": []interface{}{
			map[string]interface{}{"name": "a", "goal": "a"},
			map[string]interface{}{"name": "b", "goal": "b"},
		},
	})
	require.NoError(t, err)

	// status 不带 id:列最近计划
	out, err := manage.Execute(contextWithUser(42), map[string]interface{}{"action": "status"})
	require.NoError(t, err)
	assert.Contains(t, out, "running")
	assert.Contains(t, out, "0/2")

	// 取到 plan id 后取消
	plans, err := runner.ListByUser(42, 10)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	out, err = manage.Execute(contextWithUser(42), map[string]interface{}{"action": "cancel", "plan_id": float64(plans[0].ID)})
	require.NoError(t, err)
	assert.Contains(t, out, "cancelled")
	assert.NotEmpty(t, gw.cancelled, "取消级联当前步")
}

func TestManagePlan_NotOwned(t *testing.T) {
	delegate, manage, _, runner := setupPlanToolTest(t)
	_, err := delegate.Execute(contextWithUser(42), map[string]interface{}{
		"goal": "g",
		"steps": []interface{}{
			map[string]interface{}{"name": "a", "goal": "a"},
			map[string]interface{}{"name": "b", "goal": "b"},
		},
	})
	require.NoError(t, err)
	plans, err := runner.ListByUser(42, 10)
	require.NoError(t, err)
	require.Len(t, plans, 1)

	_, err = manage.Execute(contextWithUser(999), map[string]interface{}{"action": "cancel", "plan_id": float64(plans[0].ID)})
	require.Error(t, err, "属主隔离")
}

// 计划子任务不进单任务汇报通道(避免 9 步变 9 条消息)。
func TestNotifyCompleted_PlanChildSkipsPush(t *testing.T) {
	notifier := &mockTaskNotifier{}
	pub := &mockCompletionPublisher{}
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r"}, true)
	svc.SetNotifierForSource(domainagent.SourceFeishu, notifier)
	svc.SetCompletionPublisher(pub)

	pid := int64(9)
	child := domainagent.NewPlanChild(42, pid, 1, domainagent.NewTaskSpec("步骤"), domainagent.SourceFeishu, "ou_x")
	child.Reported = false
	require.NoError(t, repo.Create(child))
	mustTransitionSvc(t, repo, child.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(child.ID)

	assert.False(t, notifier.called, "子任务不走单任务推送")
	assert.False(t, pub.called, "子任务不走 web 实时汇报")
	got, _ := repo.GetByID(child.ID)
	assert.True(t, got.Reported)
}

// 观察者多播:周期任务 + 计划推进器共存。
func TestFinishedObserver_Multicast(t *testing.T) {
	svc, repo, _ := setupSubAgentServiceWithArtifact(t, &mockRunner{artifact: "r"}, true)
	var got []string
	svc.SetFinishedObserver(func(*domainagent.AgentTask) { got = append(got, "scheduler") })
	svc.AddFinishedObserver(func(*domainagent.AgentTask) { got = append(got, "plan") })

	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("g"), domainagent.SourceWeb, "")
	require.NoError(t, repo.Create(task))
	mustTransitionSvc(t, repo, task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)

	svc.notifyCompleted(task.ID)
	assert.ElementsMatch(t, []string{"scheduler", "plan"}, got)
}

// CreatePlanTree 一条事务建容器 + 子任务,子任务 reported=true。
func TestCreatePlanTree(t *testing.T) {
	svc, repo, _ := setupSubAgentService(t, &mockRunner{artifact: "r"})
	steps := []domainagent.PlanStepSpec{
		{Name: "a", Goal: "a"}, {Name: "b", Goal: "b"},
	}
	plan, err := svc.CreatePlanTree(context.Background(), 42, "计划", "目标", steps, domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	assert.True(t, plan.IsPlan())

	children, err := svc.ChildrenOf(plan.ID)
	require.NoError(t, err)
	require.Len(t, children, 2)
	assert.Equal(t, 1, children[0].StepSeq)
	assert.True(t, children[0].Reported)
	require.NotNil(t, children[0].ParentTaskID)
	assert.Equal(t, plan.ID, *children[0].ParentTaskID)
	_, err = repo.GetByID(plan.ID)
	require.NoError(t, err)
}
