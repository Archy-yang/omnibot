package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domain "omnibot/internal/domain/agent"
)

// 34 号(v2.12):计划树 = 容器任务 + 有序子任务。

func seedPlanTree(t *testing.T, repo AgentTaskRepository) (*domain.AgentTask, []*domain.AgentTask) {
	t.Helper()
	parentSpec := domain.NewTaskSpec("做一份云南行程")
	parentSpec.Name = "云南7日行"
	plan := domain.NewPlanTask(42, parentSpec, domain.SourceWeb, "", 0)
	children := []*domain.AgentTask{
		domain.NewPlanChild(42, 0, 1, domain.NewTaskSpec("查资料"), domain.SourceWeb, ""),
		domain.NewPlanChild(42, 0, 2, domain.NewTaskSpec("排路线"), domain.SourceWeb, ""),
	}
	require.NoError(t, repo.CreatePlan(plan, children))
	return plan, children
}

func TestCreatePlanAndListChildren(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	plan, children := seedPlanTree(t, repo)

	require.NotZero(t, plan.ID)
	got, err := repo.GetByID(plan.ID)
	require.NoError(t, err)
	assert.True(t, got.IsPlan())

	list, err := repo.ListChildren(plan.ID)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.Equal(t, 1, list[0].StepSeq)
	assert.Equal(t, 2, list[1].StepSeq)
	assert.Equal(t, plan.ID, *list[0].ParentTaskID)
	assert.True(t, list[0].Reported, "子任务创建即 reported")

	assert.Equal(t, int64(plan.ID), *children[0].ParentTaskID, "CreatePlan 回填父子关系")
}

// 任务列表只要树根(子任务折在父任务里),且按用户隔离。
func TestListRootByUser(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	seedPlanTree(t, repo)
	plain := domain.NewAgentTask(42, domain.NewTaskSpec("普通任务"), domain.SourceWeb, "")
	require.NoError(t, repo.Create(plain))
	other := domain.NewAgentTask(99, domain.NewTaskSpec("别人的"), domain.SourceWeb, "")
	require.NoError(t, repo.Create(other))

	roots, err := repo.ListByUser(42, 10)
	require.NoError(t, err)
	assert.Len(t, roots, 2, "容器 + 普通任务,不含子任务")
	for _, r := range roots {
		assert.Nil(t, r.ParentTaskID)
	}
}

// 汇报轮询只要树根:子任务永远不单独汇报。
func TestListCompletedUnreported_RootsOnly(t *testing.T) {
	db := setupAgentTaskTestDB(t)
	repo := NewAgentTaskRepository(db)
	plan, children := seedPlanTree(t, repo)

	// 子任务完成(即使 reported=false 脏数据,也不该冒出来)
	art := "步骤产出"
	ok, err := repo.TransitionStatus(children[0].ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.TransitionStatus(children[0].ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, &art, nil)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, db.Model(&domain.AgentTask{}).Where("id = ?", children[0].ID).Update("reported", false).Error)

	ok, err = repo.TransitionStatus(plan.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = repo.TransitionStatus(plan.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, &art, nil)
	require.NoError(t, err)
	require.True(t, ok)

	list, err := repo.ListCompletedUnreported(42, domain.SourceWeb)
	require.NoError(t, err)
	require.Len(t, list, 1, "只回容器,子任务不参与汇报")
	assert.Equal(t, plan.ID, list[0].ID)
}

// UpdateTaskSpec 在派发前给子任务补背景(上一步结论)。
func TestUpdateTaskSpec(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	_, children := seedPlanTree(t, repo)

	spec := domain.NewTaskSpec("排路线")
	spec.Name = "排路线"
	spec.Background = map[string]any{"上一步结果": "洱海边两晚"}
	require.NoError(t, repo.UpdateTaskSpec(children[1].ID, spec))

	got, err := repo.GetByID(children[1].ID)
	require.NoError(t, err)
	assert.Equal(t, "洱海边两晚", got.TaskSpec.Background["上一步结果"])
	assert.Equal(t, "排路线", got.Goal)
}
