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

func setupAgentTaskTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.AgentTask{})
	require.NoError(t, err)
	return db
}

func TestAgentTaskRepository_CreateAndGetByID(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))

	spec := domain.NewTaskSpec("研究 Go 1.24 新特性")
	spec.Type = "research" // 溯源标签(去角色后 taskSpec.Type → SubAgentType)
	task := domain.NewAgentTask(42, spec, "web", "")
	err := repo.Create(task)
	require.NoError(t, err)
	assert.NotZero(t, task.ID)
	assert.Equal(t, domain.TaskStatusPending, task.Status)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "research", got.SubAgentType)
	assert.Equal(t, "研究 Go 1.24 新特性", got.Goal)
	assert.Equal(t, int64(42), got.UserID)
	assert.False(t, got.Reported)
}

func TestAgentTaskRepository_UpdateStatus(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.Create(task))

	// pending -> running
	ok, err := repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusRunning, got.Status)
	assert.NotNil(t, got.StartedAt)

	// running -> completed,填 artifact
	artifact := "Go 1.24 要点:① 泛型增强 ② ..."
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, &artifact, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	got, _ = repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusCompleted, got.Status)
	assert.NotNil(t, got.Artifact)
	assert.Equal(t, artifact, *got.Artifact)
	assert.NotNil(t, got.CompletedAt)
}

func TestAgentTaskRepository_UpdateStatusFailed(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.Create(task))

	ok, err := repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)

	errMsg := "超时"
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusFailed, nil, &errMsg)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusFailed, got.Status)
	assert.NotNil(t, got.ErrorMsg)
	assert.Equal(t, "超时", *got.ErrorMsg)
	assert.Nil(t, got.Artifact)
}

func TestAgentTaskRepository_MarkReported(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, domain.NewTaskSpec("goal"), "web", "")
	require.NoError(t, repo.Create(task))
	mustTransition(t, repo, task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	mustTransition(t, repo, task.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, strPtr("result"), nil)

	got, _ := repo.GetByID(task.ID)
	assert.False(t, got.Reported)

	require.NoError(t, repo.MarkReported(task.ID))
	got, _ = repo.GetByID(task.ID)
	assert.True(t, got.Reported)
}

func TestAgentTaskRepository_ListCompletedUnreported(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))

	// 用户 1:1 个 completed 未汇报,1 个 completed 已汇报,1 个 failed 未汇报,1 个 running
	t1 := domain.NewAgentTask(1, domain.NewTaskSpec("g1"), "web", "")
	t2 := domain.NewAgentTask(1, domain.NewTaskSpec("g2"), "web", "")
	t3 := domain.NewAgentTask(1, domain.NewTaskSpec("g3"), "web", "")
	t4 := domain.NewAgentTask(1, domain.NewTaskSpec("g4"), "web", "")
	for _, tk := range []*domain.AgentTask{t1, t2, t3, t4} {
		require.NoError(t, repo.Create(tk))
	}
	mustTransition(t, repo, t1.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	mustTransition(t, repo, t1.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, strPtr("a1"), nil)
	mustTransition(t, repo, t2.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	mustTransition(t, repo, t2.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, strPtr("a2"), nil)
	require.NoError(t, repo.MarkReported(t2.ID)) // t2 已汇报
	mustTransition(t, repo, t3.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	mustTransition(t, repo, t3.ID, domain.TaskStatusRunning, domain.TaskStatusFailed, nil, strPtr("err"))
	// t4 保持 pending

	// 用户 2 的任务不应出现
	t5 := domain.NewAgentTask(2, domain.NewTaskSpec("other"), "web", "")
	require.NoError(t, repo.Create(t5))
	mustTransition(t, repo, t5.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	mustTransition(t, repo, t5.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, strPtr("a5"), nil)

	got, err := repo.ListCompletedUnreported(1)
	require.NoError(t, err)
	// 应返回 t1(completed 未汇报)+ t3(failed 未汇报),不含 t2(已汇报)/t4(pending)/t5(别的用户)
	assert.Len(t, got, 2)
	// 按完成时间升序
	assert.Equal(t, t1.ID, got[0].ID)
	assert.Equal(t, t3.ID, got[1].ID)
}

func TestAgentTaskRepository_ListByUser(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	for i := 0; i < 3; i++ {
		require.NoError(t, repo.Create(domain.NewAgentTask(1, domain.NewTaskSpec("g"), "web", "")))
	}
	require.NoError(t, repo.Create(domain.NewAgentTask(2, domain.NewTaskSpec("other"), "web", "")))

	got, err := repo.ListByUser(1, 10)
	require.NoError(t, err)
	assert.Len(t, got, 3) // 不含用户 2
	// 倒序(最新在前)
}

// TestAgentTaskRepository_CancelTransition 取消任务(CAS running→cancelled):置 cancelled + cancelled_at。
func TestAgentTaskRepository_CancelTransition(t *testing.T) {
	db := setupAgentTaskTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))
	ok, err := repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCancelled, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	require.True(t, ok)
	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.TaskStatusCancelled, got.Status)
	assert.NotNil(t, got.CancelledAt)
}

// TestAgentTaskRepository_UpdateGoal 改 goal(pending 态 update_task 用)。
func TestAgentTaskRepository_UpdateGoal(t *testing.T) {
	db := setupAgentTaskTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("旧 goal"), "web", "")
	require.NoError(t, repo.Create(task))

	require.NoError(t, repo.UpdateGoal(task.ID, "新 goal"))
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, "新 goal", got.Goal)
}

// TestAgentTaskRepository_AppendNote 追加 notes(running 态补充信息)。
func TestAgentTaskRepository_AppendNote(t *testing.T) {
	db := setupAgentTaskTestDB(t)
	repo := NewAgentTaskRepository(db)
	task := domain.NewAgentTask(1, domain.NewTaskSpec("g"), "web", "")
	require.NoError(t, repo.Create(task))

	require.NoError(t, repo.AppendNote(task.ID, "补充1"))
	require.NoError(t, repo.AppendNote(task.ID, "补充2"))
	got, _ := repo.GetByID(task.ID)
	require.Len(t, got.Notes, 2)
	assert.Equal(t, "补充1", got.Notes[0])
	assert.Equal(t, "补充2", got.Notes[1])
}

func strPtr(s string) *string { return &s }

// ---- Phase 5:CAS 状态迁移(16-架构迭代路线图 §11/§12) ----

// TestTransitionStatus_CAS 核心竞态防护:终态一旦落定不可被覆盖。
// 场景:任务被取消后,迟到的完成/失败写入必须失败(旧行为是无条件 SET 覆盖)。
func TestTransitionStatus_CAS(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))

	spec := domain.NewTaskSpec("调研 X")
	task := domain.NewAgentTask(42, spec, "web", "")
	require.NoError(t, repo.Create(task))

	// pending → running:成功
	ok, err := repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	assert.True(t, ok, "pending→running 应成功")
	got, _ := repo.GetByID(task.ID)
	require.NotNil(t, got.StartedAt, "running 迁移应记 started_at")

	// running → cancelled(用户取消先到)
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCancelled, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	assert.True(t, ok)
	got, _ = repo.GetByID(task.ID)
	require.NotNil(t, got.CancelledAt, "cancelled 迁移应记 cancelled_at")

	// 迟到的完成写入:必须失败,状态保持 cancelled(旧行为会被覆盖成 completed)
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, strPtrRepo("late artifact"), nil)
	require.NoError(t, err)
	assert.False(t, ok, "cancelled 后的 running→completed 必须被 CAS 拒绝")
	got, _ = repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusCancelled, got.Status)
	assert.Nil(t, got.Artifact, "被拒绝的迁移不得写入 artifact")

	// completed 后再取消:同样拒绝
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCancelled, nil, nil)
	require.NoError(t, err)
	assert.False(t, ok)

	// 非法迁移(违反状态机):即使 DB 层没有守卫也直接拒绝
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusCompleted, nil, nil)
	require.NoError(t, err)
	assert.False(t, ok, "违反状态机表的迁移直接拒绝")
}

// TestTransitionStatus_CompletedTimestamps completed/failed 迁移记 completed_at + 附加字段。
func TestTransitionStatus_CompletedTimestamps(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))

	spec := domain.NewTaskSpec("调研 X")
	task := domain.NewAgentTask(42, spec, "web", "")
	require.NoError(t, repo.Create(task))
	ok, err := repo.TransitionStatus(task.ID, domain.TaskStatusPending, domain.TaskStatusRunning, nil, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	require.True(t, ok)

	artifact := "最终产出"
	ok, err = repo.TransitionStatus(task.ID, domain.TaskStatusRunning, domain.TaskStatusCompleted, &artifact, nil)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	require.True(t, ok)
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusCompleted, got.Status)
	require.NotNil(t, got.Artifact)
	assert.Equal(t, "最终产出", *got.Artifact)
	require.NotNil(t, got.CompletedAt, "completed 迁移应记 completed_at")
}

func strPtrRepo(s string) *string { return &s }

// mustTransition 测试辅助:CAS 迁移并断言成功(Phase 5)。
func mustTransition(t *testing.T, repo AgentTaskRepository, id int64, from, to string, artifact, errMsg *string) {
	t.Helper()
	ok, err := repo.TransitionStatus(id, from, to, artifact, errMsg)
	require.True(t, ok, "迁移应成功")
	require.NoError(t, err)
	require.True(t, ok, "%s→%s 迁移应成功", from, to)
}
