package agent

// persistent_task_scheduler_test.go — 25 号调度器测试盲区补齐:
// Recover(重算/坏 cron 停用)、TriggerOne(CAS 抢占/派活/回填)、失败累计(auto-pause)。
// 真实 repo(sqlite)+ 真实 service,只 stub TaskStarter——调度器与持久层的合同一起验。

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
)

type stubStarter struct {
	called int
	userID int64
	source string
	notify string
	taskID int64
	err    error
}

func (s *stubStarter) StartTask(_ context.Context, userID int64, _ domainagent.TaskSpec, source, notifyTarget string, _ int64) (int64, error) {
	s.called++
	s.userID = userID
	s.source = source
	s.notify = notifyTarget
	if s.err != nil {
		return 0, s.err
	}
	s.taskID++
	return s.taskID, nil
}

func setupSchedulerTest(t *testing.T) (*PersistentTaskScheduler, *stubStarter, repoagent.PersistentTaskRepository, *gorm.DB) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domainagent.PersistentTask{}, &domainagent.AgentTask{}))
	ptRepo := repoagent.NewPersistentTaskRepository(db)
	svc := NewPersistentTaskService(ptRepo)
	scheduler := NewPersistentTaskScheduler(ptRepo, svc)
	starter := &stubStarter{}
	scheduler.WireStarter(starter)
	return scheduler, starter, ptRepo, db
}

func dueTask(userID int64) *domainagent.PersistentTask {
	specJSON, _ := json.Marshal(domainagent.NewTaskSpec("抓 AI 资讯"))
	return &domainagent.PersistentTask{
		UserID: userID, Name: "抓资讯", Goal: "抓 AI 资讯",
		Cron: "0 9 * * *", Enabled: true, NextRunAt: time.Now().Add(-time.Minute),
		Source: "web", NotifyTarget: "", TaskSpecJSON: string(specJSON),
	}
}

func TestPersistentTaskScheduler_Recover(t *testing.T) {
	scheduler, _, ptRepo, _ := setupSchedulerTest(t)
	// 正常任务:next_run_at 停在过去 → Recover 重算到未来
	due := dueTask(1)
	due.NextRunAt = time.Now().Add(-24 * time.Hour)
	require.NoError(t, ptRepo.Create(due))
	// 坏 cron 任务:Recover 应停用而非卡死
	bad := dueTask(2)
	bad.Cron = "not-a-cron"
	require.NoError(t, ptRepo.Create(bad))

	require.NoError(t, scheduler.Recover(time.Now()))

	got, err := ptRepo.GetByID(due.ID)
	require.NoError(t, err)
	assert.True(t, got.NextRunAt.After(time.Now()), "正常任务 next_run_at 应重算到未来")

	gotBad, err := ptRepo.GetByID(bad.ID)
	require.NoError(t, err)
	assert.False(t, gotBad.Enabled, "坏 cron 应被停用")
}

func TestPersistentTaskScheduler_TriggerOne_CASAndDispatch(t *testing.T) {
	scheduler, starter, ptRepo, _ := setupSchedulerTest(t)
	due := dueTask(7)
	require.NoError(t, ptRepo.Create(due))

	ok, err := scheduler.TriggerOne(due)
	require.NoError(t, err)
	require.True(t, ok)

	assert.Equal(t, 1, starter.called)
	assert.Equal(t, int64(7), starter.userID)
	assert.Equal(t, "web", starter.source, "实例继承创建渠道 source(汇报去向)")

	got, err := ptRepo.GetByID(due.ID)
	require.NoError(t, err)
	assert.True(t, got.NextRunAt.After(time.Now()), "next_run_at 已推进")
	assert.NotNil(t, got.LastTaskID, "last_task_id 已回填")
}

func TestPersistentTaskScheduler_TriggerOne_CASLost(t *testing.T) {
	scheduler, starter, ptRepo, _ := setupSchedulerTest(t)
	due := dueTask(7)
	require.NoError(t, ptRepo.Create(due))

	// 用过期的 NextRunAt 抢占 → CAS 失败,不派活
	staleNext := due.NextRunAt.Add(-time.Hour)
	ok, err := scheduler.TriggerOne(&domainagent.PersistentTask{
		ID: due.ID, UserID: 7, Cron: due.Cron, Enabled: true, NextRunAt: staleNext,
	})
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Zero(t, starter.called, "CAS 失败不派活")
}

func TestPersistentTaskScheduler_FailStreakAutoPause(t *testing.T) {
	scheduler, _, ptRepo, _ := setupSchedulerTest(t)
	due := dueTask(7)
	require.NoError(t, ptRepo.Create(due))

	// 规格损坏 → HandleFinished 按失败计;连续 3 次自动停用
	// (直接改 DB 行:内存改 struct 不影响 TriggerOne 从 DB 读的 spec)
	require.NoError(t, ptRepo.UpdateFields(due.ID, map[string]interface{}{"task_spec_json": "{invalid json"}))
	for i := 0; i < domainagent.AutoPauseFailStreak; i++ {
		dueNext, err := ptRepo.GetByID(due.ID)
		require.NoError(t, err)
		_, _ = scheduler.TriggerOne(dueNext)
	}
	got, err := ptRepo.GetByID(due.ID)
	require.NoError(t, err)
	assert.Equal(t, domainagent.AutoPauseFailStreak, got.FailStreak)
	assert.False(t, got.Enabled, "连续失败达阈值应自动停用(且不发通知)")
}
