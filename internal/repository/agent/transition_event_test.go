package agent

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/agent"
)

// Phase 7a(16-架构迭代路线图 §14):Task Transition + Append Event 同一事务,
// 事件序号由 agent_tasks.version 派生(不再依赖进程内 eventSeq map),
// (task_id, sequence) 唯一约束保证幂等。

func setupTransitionEventTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.AgentTask{}, &domain.TaskEvent{}))
	return db
}

// TestCreateWithEvent 建任务与 submitted 事件同一事务落库:
// 任务 version=1,事件 sequence=1(版本派生序号的起点)。
func TestCreateWithEvent(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	repo := NewAgentTaskRepository(db)

	task := domain.NewAgentTask(42, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.CreateWithEvent(task, domain.EventTaskSubmitted, "main"))
	require.NotZero(t, task.ID)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Version, "创建即 submitted 迁移,version 应为 1")

	events, err := NewTaskEventRepository(db).ListByTaskID(task.ID)
	require.NoError(t, err)
	require.Len(t, events, 1)
	assert.Equal(t, domain.EventTaskSubmitted, events[0].EventType)
	assert.Equal(t, 1, events[0].Sequence)
	assert.Equal(t, "main", events[0].SourceAgent)
}

// TestTransitionStatusWithEvent_Atomic 迁移与事件同事务:
// 状态变 + version+1 + 事件 sequence=新 version,三者原子。
func TestTransitionStatusWithEvent_Atomic(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.CreateWithEvent(task, domain.EventTaskSubmitted, "main"))

	ok, err := repo.TransitionStatusWithEvent(task.ID,
		domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil,
		domain.EventTaskRunning, "sub")
	require.NoError(t, err)
	require.True(t, ok)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusRunning, got.Status)
	assert.Equal(t, int64(2), got.Version, "第二次迁移,version 应为 2")

	events, err := NewTaskEventRepository(db).ListByTaskID(task.ID)
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, domain.EventTaskRunning, events[1].EventType)
	assert.Equal(t, 2, events[1].Sequence, "事件序号应等于迁移后的 version")
	assert.Equal(t, "sub", events[1].SourceAgent)
}

// TestTransitionStatusWithEvent_CASFail_NoEvent CAS 失败(状态已被并发改变):
// 迁移不发生、version 不变、不写事件——事务整体回滚。
func TestTransitionStatusWithEvent_CASFail_NoEvent(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.CreateWithEvent(task, domain.EventTaskSubmitted, "main"))
	// 先迁移到 running,再尝试从 pending 迁移(必然 CAS 失败)
	require.True(t, mustTransitionEvent(t, repo, task.ID, domain.TaskStatusPending, domain.TaskStatusRunning))

	ok, err := repo.TransitionStatusWithEvent(task.ID,
		domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil,
		domain.EventTaskRunning, "sub")
	require.NoError(t, err)
	assert.False(t, ok)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), got.Version, "CAS 失败不得推进 version")

	events, err := NewTaskEventRepository(db).ListByTaskID(task.ID)
	require.NoError(t, err)
	assert.Len(t, events, 2, "CAS 失败不得产生事件")
}

// TestTransitionStatusWithEvent_IllegalTransition_NoEvent 非法迁移(状态机拒绝):
// 不写事件、不推进 version。
func TestTransitionStatusWithEvent_IllegalTransition_NoEvent(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.CreateWithEvent(task, domain.EventTaskSubmitted, "main"))

	ok, err := repo.TransitionStatusWithEvent(task.ID,
		domain.TaskStatusPending, domain.TaskStatusCompleted, nil, nil,
		domain.EventTaskCompleted, "sub")
	require.NoError(t, err)
	assert.False(t, ok)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Version)

	events, err := NewTaskEventRepository(db).ListByTaskID(task.ID)
	require.NoError(t, err)
	assert.Len(t, events, 1, "非法迁移不得产生事件")
}

// TestTaskEvent_UniqueTaskSequence (task_id, sequence) 唯一约束:
// 同任务同序号重复插入必须被拒(幂等防线,进程内 map 删除后的兜底)。
func TestTaskEvent_UniqueTaskSequence(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, NewAgentTaskRepository(db).CreateWithEvent(task, domain.EventTaskSubmitted, "main"))

	dup := domain.NewTaskEvent(task.ID, domain.EventTaskRunning, 1, "sub") // sequence=1 已存在
	err := db.Create(&dup).Error
	require.Error(t, err, "(task_id, sequence) 重复插入必须被唯一约束拒绝")
}

// TestTransitionStatus_BumpsVersion 无事件迁移(审计适配器等旧路径)同样推进 version,
// 保证 version 语义 = 状态变更次数(事件序号允许跳号,不要求连续)。
func TestTransitionStatus_BumpsVersion(t *testing.T) {
	db := setupTransitionEventTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.Create(task))
	require.Equal(t, int64(0), mustVersion(t, repo, task.ID), "普通 Create version=0(无事件路径)")

	require.True(t, mustTransitionEvent(t, repo, task.ID, domain.TaskStatusPending, domain.TaskStatusRunning))
	assert.Equal(t, int64(1), mustVersion(t, repo, task.ID))
}

// ---- helpers ----

func mustTransitionEvent(t *testing.T, repo AgentTaskRepository, id int64, from, to string) bool {
	t.Helper()
	ok, err := repo.TransitionStatusWithEvent(id, from, to, nil, nil, "task."+to, "sub")
	require.NoError(t, err)
	return ok
}

func mustVersion(t *testing.T, repo AgentTaskRepository, id int64) int64 {
	t.Helper()
	got, err := repo.GetByID(id)
	require.NoError(t, err)
	return got.Version
}
