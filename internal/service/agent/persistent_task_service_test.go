package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	domainagent "omnibot/internal/domain/agent"
	agentrepo "omnibot/internal/repository/agent"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 25 号:持久任务服务 + 调度器。宿主管"何时派活",执行/汇报链路零改动。

func persistentSetup(t *testing.T) (*PersistentTaskService, *PersistentTaskScheduler, agentrepo.PersistentTaskRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}, &domainagent.PersistentTask{}))

	repo := agentrepo.NewPersistentTaskRepository(db)
	svc := NewPersistentTaskService(repo)
	sched := NewPersistentTaskScheduler(repo, svc)
	return svc, sched, repo, db
}

// T2 Create 校验:非法 cron 报错 / goal 必填 / 上限 20 / next_run_at 正确计算。
func TestPersistentTaskService_Create(t *testing.T) {
	svc, _, _, _ := persistentSetup(t)

	// 非法 cron
	_, _, err := svc.Create(1, CreatePersistentTaskInput{Name: "n", Goal: "g", Cron: "not a cron"})
	require.Error(t, err)

	// goal 必填
	_, _, err = svc.Create(1, CreatePersistentTaskInput{Name: "n", Goal: "", Cron: "0 9 * * 1"})
	require.Error(t, err)

	// 合法创建:每周一 9 点 → next 落在未来的周一 9 点
	id, next, err := svc.Create(1, CreatePersistentTaskInput{Name: "抓资讯", Goal: "抓 AI 资讯", Cron: "0 9 * * 1"})
	require.NoError(t, err)
	assert.Positive(t, id)
	assert.Equal(t, time.Monday, next.Weekday())
	assert.Equal(t, 9, next.Hour())
	assert.True(t, next.After(time.Now()))

	// 上限 20
	for i := 0; i < domainagent.MaxPersistentTasksPerUser-1; i++ {
		_, _, err = svc.Create(1, CreatePersistentTaskInput{Goal: "g", Cron: "*/5 * * * *"})
		require.NoError(t, err)
	}
	_, _, err = svc.Create(1, CreatePersistentTaskInput{Goal: "g", Cron: "*/5 * * * *"})
	require.Error(t, err, "超上限拒绝")

	// 其他用户不受该上限影响
	_, _, err = svc.Create(2, CreatePersistentTaskInput{Goal: "g", Cron: "*/5 * * * *"})
	require.NoError(t, err)
}

// T3 归属隔离:Pause/Resume/Delete 未命中或他人任务 → ErrPersistentTaskNotFound;resume 重算 next。
func TestPersistentTaskService_OwnershipAndResume(t *testing.T) {
	svc, _, repo, _ := persistentSetup(t)
	id, _, err := svc.Create(1, CreatePersistentTaskInput{Goal: "g", Cron: "0 9 * * 1"})
	require.NoError(t, err)

	// 他人操作 → not found
	assert.ErrorIs(t, svc.Pause(99, id), ErrPersistentTaskNotFound)
	assert.ErrorIs(t, svc.Resume(99, id), ErrPersistentTaskNotFound)
	assert.ErrorIs(t, svc.Delete(99, id), ErrPersistentTaskNotFound)
	assert.ErrorIs(t, svc.Resume(1, 99999), ErrPersistentTaskNotFound)

	// pause → enabled=false
	require.NoError(t, svc.Pause(1, id))
	pt, err := repo.GetByID(id)
	require.NoError(t, err)
	assert.False(t, pt.Enabled)

	// resume → enabled=true 且 next_run_at 重算到未来
	stale := time.Now().Add(-time.Hour)
	require.NoError(t, repo.UpdateFields(id, map[string]interface{}{"next_run_at": stale}))
	require.NoError(t, svc.Resume(1, id))
	pt, err = repo.GetByID(id)
	require.NoError(t, err)
	assert.True(t, pt.Enabled)
	assert.True(t, pt.NextRunAt.After(time.Now()), "resume 重算 next_run_at")

	require.NoError(t, svc.Delete(1, id))
	_, err = repo.GetByID(id)
	assert.Error(t, err)
}

// T4 HandleFinished:成功清零 / 失败累加 / ≥3 自动停用 / 非调度实例与取消忽略。
func TestPersistentTaskService_HandleFinished(t *testing.T) {
	svc, _, repo, _ := persistentSetup(t)
	id, _, err := svc.Create(1, CreatePersistentTaskInput{Goal: "g", Cron: "*/5 * * * *"})
	require.NoError(t, err)

	failed := func() *domainagent.AgentTask {
		tid := int64(100)
		return &domainagent.AgentTask{ID: tid, Status: domainagent.TaskStatusFailed, ScheduleID: &id}
	}

	// 非调度实例忽略
	svc.HandleFinished(&domainagent.AgentTask{ID: 1, Status: domainagent.TaskStatusFailed})
	pt, _ := repo.GetByID(id)
	assert.Equal(t, 0, pt.FailStreak)
	assert.True(t, pt.Enabled)

	// 取消忽略
	svc.HandleFinished(&domainagent.AgentTask{Status: domainagent.TaskStatusCancelled, ScheduleID: &id})
	pt, _ = repo.GetByID(id)
	assert.Equal(t, 0, pt.FailStreak)

	// 两次失败:累加,仍启用
	svc.HandleFinished(failed())
	svc.HandleFinished(failed())
	pt, _ = repo.GetByID(id)
	assert.Equal(t, 2, pt.FailStreak)
	assert.True(t, pt.Enabled)

	// 第三次失败:自动停用
	svc.HandleFinished(failed())
	pt, _ = repo.GetByID(id)
	assert.Equal(t, 3, pt.FailStreak)
	assert.False(t, pt.Enabled, "连续失败 ≥3 自动停用")

	// 成功清零(resume 后)
	require.NoError(t, svc.Resume(1, id))
	svc.HandleFinished(&domainagent.AgentTask{Status: domainagent.TaskStatusCompleted, ScheduleID: &id})
	pt, _ = repo.GetByID(id)
	assert.Equal(t, 0, pt.FailStreak)
	assert.True(t, pt.Enabled)
}

// fakeStarter 记录 StartTask 调用。
type fakeStarter struct {
	calls []startCall
	fail  bool
}

type startCall struct {
	userID int64
	spec   domainagent.TaskSpec
	source string
	target string
}

func (f *fakeStarter) StartTask(_ context.Context, userID int64, taskSpec domainagent.TaskSpec, source, notifyTarget string, _ int64) (int64, error) {
	if f.fail {
		return 0, errFakeStart
	}
	f.calls = append(f.calls, startCall{userID, taskSpec, source, notifyTarget})
	return int64(1000 + len(f.calls)), nil
}

var errFakeStart = assert.AnError

// T5 调度器:due 任务派活(合同反序列化/继承 notify_target/回填 last_task_id/CAS 推进 next)。
func TestScheduler_TriggersDueTasks(t *testing.T) {
	svc, sched, repo, _ := persistentSetup(t)
	spec := domainagent.NewTaskSpec("抓 AI 资讯")
	spec.Name = "抓资讯"
	specRaw, err := json.Marshal(spec)
	require.NoError(t, err)
	id, _, err := svc.Create(1, CreatePersistentTaskInput{
		Name: "抓资讯", Goal: "抓 AI 资讯", Cron: "*/5 * * * *",
		Source: "feishu", NotifyTarget: "ou_123", SpecJSON: string(specRaw),
	})
	require.NoError(t, err)

	starter := &fakeStarter{}
	sched.starter = starter

	// 人为把 next_run_at 拨到过期,构造"到期"(创建时 next 是最近的 5 分钟刻度,尚在未来)
	require.NoError(t, repo.UpdateFields(id, map[string]interface{}{"next_run_at": time.Now().Add(-time.Minute)}))

	due, err := repo.ListDue(time.Now(), 50)
	require.NoError(t, err)
	require.Len(t, due, 1)
	triggered, err := sched.TriggerOne(due[0])
	require.NoError(t, err)
	assert.True(t, triggered)

	require.Len(t, starter.calls, 1)
	call := starter.calls[0]
	assert.Equal(t, int64(1), call.userID)
	assert.Equal(t, "抓 AI 资讯", call.spec.Goal)
	assert.Equal(t, "feishu", call.source, "实例继承创建渠道 source(决定汇报去向)")
	assert.Equal(t, "ou_123", call.target, "继承 notify_target")

	// 调度状态推进:last_task_id 回填,next_run_at 推进到未来,last_run_at 填写
	pt, err := repo.GetByID(id)
	require.NoError(t, err)
	require.NotNil(t, pt.LastTaskID)
	assert.Equal(t, int64(1001), *pt.LastTaskID)
	assert.True(t, pt.NextRunAt.After(time.Now()))
	require.NotNil(t, pt.LastRunAt)

	// 已推进的不再是 due
	due, err = repo.ListDue(time.Now(), 50)
	require.NoError(t, err)
	assert.Empty(t, due)
}

// T5b 恢复:启动恢复把过期的 next_run_at 重算到未来,错过的不追帧。
func TestScheduler_RecoverReschedules(t *testing.T) {
	svc, sched, repo, _ := persistentSetup(t)
	id, _, err := svc.Create(1, CreatePersistentTaskInput{Goal: "g", Cron: "0 9 * * 1"})
	require.NoError(t, err)
	stale := time.Now().Add(-72 * time.Hour)
	require.NoError(t, repo.UpdateFields(id, map[string]interface{}{"next_run_at": stale}))

	require.NoError(t, sched.Recover(time.Now()))

	pt, err := repo.GetByID(id)
	require.NoError(t, err)
	assert.True(t, pt.NextRunAt.After(time.Now()), "停机错过的触发丢弃,只跑下一次")

	// 恢复后无 due
	due, _ := repo.ListDue(time.Now(), 50)
	assert.Empty(t, due)
}

// T6 manage_persistent_task 工具:create/list/pause/resume/delete 全链路。
func TestManagePersistentTaskTool(t *testing.T) {
	svc, _, repo, _ := persistentSetup(t)
	tool := CreateManagePersistentTaskTool(svc)
	assert.Equal(t, "manage_persistent_task", tool.Name)

	ctx := withUserID(context.Background(), 1)

	// create
	out, err := tool.Execute(ctx, map[string]interface{}{
		"action": "create", "goal": "抓 AI 资讯", "cron": "0 9 * * 1", "name": "抓资讯",
	})
	require.NoError(t, err)
	assert.Contains(t, out, "已创建")
	assert.Contains(t, out, "下次执行", "确认文案带下次执行时间")

	// create 非法 cron
	_, err = tool.Execute(ctx, map[string]interface{}{"action": "create", "goal": "g", "cron": "bad"})
	require.Error(t, err)

	// list
	out, err = tool.Execute(ctx, map[string]interface{}{"action": "list"})
	require.NoError(t, err)
	assert.Contains(t, out, "抓资讯")

	// pause → resume → delete
	pts, err := svc.List(1)
	require.NoError(t, err)
	require.Len(t, pts, 1)
	pid := pts[0].ID

	_, err = tool.Execute(ctx, map[string]interface{}{"action": "pause", "id": float64(pid)})
	require.NoError(t, err)
	pt, _ := repo.GetByID(pid)
	assert.False(t, pt.Enabled)

	out, err = tool.Execute(ctx, map[string]interface{}{"action": "resume", "id": float64(pid)})
	require.NoError(t, err)
	pt, _ = repo.GetByID(pid)
	assert.True(t, pt.Enabled)
	assert.True(t, strings.Contains(out, "已恢复"))

	_, err = tool.Execute(ctx, map[string]interface{}{"action": "delete", "id": float64(pid)})
	require.NoError(t, err)
	pts, _ = svc.List(1)
	assert.Empty(t, pts)

	// 他人 list 看不到(归属隔离)
	_, err = tool.Execute(context.Background(), map[string]interface{}{"action": "list"})
	require.Error(t, err, "无用户上下文拒绝")
}
