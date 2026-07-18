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

	task := domain.NewAgentTask(42, "researcher", "研究 Go 1.24 新特性")
	err := repo.Create(task)
	require.NoError(t, err)
	assert.NotZero(t, task.ID)
	assert.Equal(t, domain.TaskStatusPending, task.Status)

	got, err := repo.GetByID(task.ID)
	require.NoError(t, err)
	assert.Equal(t, "researcher", got.SubAgentType)
	assert.Equal(t, "研究 Go 1.24 新特性", got.Goal)
	assert.Equal(t, int64(42), got.UserID)
	assert.False(t, got.Reported)
}

func TestAgentTaskRepository_UpdateStatus(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, "researcher", "goal")
	require.NoError(t, repo.Create(task))

	// pending -> running
	err := repo.UpdateStatus(task.ID, domain.TaskStatusRunning, nil, nil)
	require.NoError(t, err)
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusRunning, got.Status)
	assert.NotNil(t, got.StartedAt)

	// running -> completed,填 artifact
	artifact := "Go 1.24 要点:① 泛型增强 ② ..."
	err = repo.UpdateStatus(task.ID, domain.TaskStatusCompleted, &artifact, nil)
	require.NoError(t, err)
	got, _ = repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusCompleted, got.Status)
	assert.NotNil(t, got.Artifact)
	assert.Equal(t, artifact, *got.Artifact)
	assert.NotNil(t, got.CompletedAt)
}

func TestAgentTaskRepository_UpdateStatusFailed(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, "researcher", "goal")
	require.NoError(t, repo.Create(task))

	errMsg := "超时"
	err := repo.UpdateStatus(task.ID, domain.TaskStatusFailed, nil, &errMsg)
	require.NoError(t, err)
	got, _ := repo.GetByID(task.ID)
	assert.Equal(t, domain.TaskStatusFailed, got.Status)
	assert.NotNil(t, got.ErrorMsg)
	assert.Equal(t, "超时", *got.ErrorMsg)
	assert.Nil(t, got.Artifact)
}

func TestAgentTaskRepository_MarkReported(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))
	task := domain.NewAgentTask(1, "researcher", "goal")
	require.NoError(t, repo.Create(task))
	require.NoError(t, repo.UpdateStatus(task.ID, domain.TaskStatusCompleted, strPtr("result"), nil))

	got, _ := repo.GetByID(task.ID)
	assert.False(t, got.Reported)

	require.NoError(t, repo.MarkReported(task.ID))
	got, _ = repo.GetByID(task.ID)
	assert.True(t, got.Reported)
}

func TestAgentTaskRepository_ListCompletedUnreported(t *testing.T) {
	repo := NewAgentTaskRepository(setupAgentTaskTestDB(t))

	// 用户 1:1 个 completed 未汇报,1 个 completed 已汇报,1 个 failed 未汇报,1 个 running
	t1 := domain.NewAgentTask(1, "researcher", "g1")
	t2 := domain.NewAgentTask(1, "researcher", "g2")
	t3 := domain.NewAgentTask(1, "researcher", "g3")
	t4 := domain.NewAgentTask(1, "researcher", "g4")
	for _, tk := range []*domain.AgentTask{t1, t2, t3, t4} {
		require.NoError(t, repo.Create(tk))
	}
	require.NoError(t, repo.UpdateStatus(t1.ID, domain.TaskStatusCompleted, strPtr("a1"), nil))
	require.NoError(t, repo.UpdateStatus(t2.ID, domain.TaskStatusCompleted, strPtr("a2"), nil))
	require.NoError(t, repo.MarkReported(t2.ID)) // t2 已汇报
	require.NoError(t, repo.UpdateStatus(t3.ID, domain.TaskStatusFailed, nil, strPtr("err")))
	// t4 保持 pending

	// 用户 2 的任务不应出现
	t5 := domain.NewAgentTask(2, "researcher", "other")
	require.NoError(t, repo.Create(t5))
	require.NoError(t, repo.UpdateStatus(t5.ID, domain.TaskStatusCompleted, strPtr("a5"), nil))

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
		require.NoError(t, repo.Create(domain.NewAgentTask(1, "researcher", "g")))
	}
	require.NoError(t, repo.Create(domain.NewAgentTask(2, "researcher", "other")))

	got, err := repo.ListByUser(1, 10)
	require.NoError(t, err)
	assert.Len(t, got, 3) // 不含用户 2
	// 倒序(最新在前)
}

func strPtr(s string) *string { return &s }
