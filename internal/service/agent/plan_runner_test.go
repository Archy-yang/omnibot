package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// 34 号(v2.12 合并建模):计划=容器任务,步骤=子任务。
// 推进器只依赖窄接口,测试用内存替身,不碰 DB/真实执行器。

type fakePlanStore struct {
	mu    sync.Mutex
	tasks map[int64]*domainagent.AgentTask
	next  int64
}

func newFakePlanStore() *fakePlanStore {
	return &fakePlanStore{tasks: map[int64]*domainagent.AgentTask{}, next: 100}
}

func (f *fakePlanStore) add(t *domainagent.AgentTask) *domainagent.AgentTask {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	t.ID = f.next
	cp := *t
	f.tasks[t.ID] = &cp
	return &cp
}

func (f *fakePlanStore) GetByID(id int64) (*domainagent.AgentTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (f *fakePlanStore) TransitionStatus(id int64, from, to string, artifact *string, errMsg *string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t, ok := f.tasks[id]
	if !ok || t.Status != from || !domainagent.CanTransition(from, to) {
		return false, nil
	}
	t.Status = to
	if artifact != nil {
		t.Artifact = artifact
	}
	if errMsg != nil {
		t.ErrorMsg = errMsg
	}
	return true, nil
}

func (f *fakePlanStore) UpdateTaskSpec(id int64, spec domainagent.TaskSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if t, ok := f.tasks[id]; ok {
		t.TaskSpec = spec
		t.Goal = spec.Goal
	}
	return nil
}

func (f *fakePlanStore) ListRootByStatus(status string) ([]*domainagent.AgentTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domainagent.AgentTask
	for _, t := range f.tasks {
		if t.Status == status && t.ParentTaskID == nil {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *fakePlanStore) ListByUser(userID int64, limit int) ([]*domainagent.AgentTask, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domainagent.AgentTask
	for _, t := range f.tasks {
		if t.UserID == userID && t.ParentTaskID == nil {
			cp := *t
			out = append(out, &cp)
		}
	}
	return out, nil
}

type fakePlanGateway struct {
	store     *fakePlanStore
	launched  []int64
	reset     []int64
	cancelled []int64
	notified  []int64
	children  map[int64][]int64 // planID -> child ids
}

func newFakePlanGateway(store *fakePlanStore) *fakePlanGateway {
	return &fakePlanGateway{store: store, children: map[int64][]int64{}}
}

func (g *fakePlanGateway) CreatePlanTree(_ context.Context, userID int64, name, goal string, steps []domainagent.PlanStepSpec, source, notifyTarget string, originTurnID int64) (*domainagent.AgentTask, error) {
	spec := domainagent.NewTaskSpec(goal)
	spec.Name = name
	plan := g.store.add(domainagent.NewPlanTask(userID, spec, source, notifyTarget, originTurnID))
	var ids []int64
	for i, st := range steps {
		childSpec := domainagent.PlanStepSpecFor(plan, st, i+1, len(steps), "")
		c := g.store.add(domainagent.NewPlanChild(userID, plan.ID, i+1, childSpec, source, notifyTarget))
		ids = append(ids, c.ID)
	}
	g.children[plan.ID] = ids
	return plan, nil
}

func (g *fakePlanGateway) LaunchChild(_ context.Context, childID int64) error {
	g.launched = append(g.launched, childID)
	return nil
}

func (g *fakePlanGateway) ChildrenOf(planID int64) ([]*domainagent.AgentTask, error) {
	var out []*domainagent.AgentTask
	for _, id := range g.children[planID] {
		t, _ := g.store.GetByID(id)
		out = append(out, t)
	}
	return out, nil
}

func (g *fakePlanGateway) ResetChildForRetry(childID int64) error {
	g.reset = append(g.reset, childID)
	g.store.mu.Lock()
	defer g.store.mu.Unlock()
	if stored, ok := g.store.tasks[childID]; ok {
		stored.Status = domainagent.TaskStatusPending
		stored.ErrorMsg = nil
		stored.Attempts++
	}
	return nil
}

func (g *fakePlanGateway) CancelTask(_ int64, taskID int64) error {
	g.cancelled = append(g.cancelled, taskID)
	return nil
}

func (g *fakePlanGateway) NotifyFinished(taskID int64) {
	g.notified = append(g.notified, taskID)
}

func newRunnerFixture(t *testing.T) (*PlanRunner, *fakePlanStore, *fakePlanGateway) {
	t.Helper()
	store := newFakePlanStore()
	gw := newFakePlanGateway(store)
	return NewPlanRunner(store, gw), store, gw
}

func planSteps() []domainagent.PlanStepSpec {
	return []domainagent.PlanStepSpec{
		{Name: "查资料", Goal: "查目的地"},
		{Name: "排路线", Goal: "排 7 天路线"},
		{Name: "配住宿", Goal: "配住宿"},
	}
}

// finishChild 模拟子任务终态回调。
func finishChild(t *testing.T, store *fakePlanStore, childID int64, status, artifact string) *domainagent.AgentTask {
	t.Helper()
	store.mu.Lock()
	task := store.tasks[childID]
	store.mu.Unlock()
	require.NotNil(t, task)
	if status == domainagent.TaskStatusCompleted {
		task.Artifact = &artifact
	}
	if status == domainagent.TaskStatusFailed {
		msg := "工具超时"
		task.ErrorMsg = &msg
	}
	task.Status = status
	return task
}

func TestPlanRunner_CreateAndStart(t *testing.T) {
	r, _, gw := newRunnerFixture(t)

	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	assert.True(t, plan.IsPlan())
	assert.Equal(t, domainagent.TaskStatusRunning, plan.Status, "创建即启动")
	require.Len(t, gw.launched, 1, "启动后派第一步")
}

func TestPlanRunner_AdvanceInjectsPrevArtifact(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	first := finishChild(t, store, children[0], domainagent.TaskStatusCompleted, "大理古城两晚")
	r.OnTaskFinished(first)

	require.Len(t, gw.launched, 2, "第一步完成 → 派第二步")
	second, _ := store.GetByID(children[1])
	prev, _ := second.TaskSpec.Background["上一步结果"].(string)
	assert.Equal(t, "大理古城两晚", prev, "上一步结论注入下一步背景")
	assert.Equal(t, "第 2/3 步", second.TaskSpec.Background["进度"])
}

func TestPlanRunner_CompletePlan(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	for _, id := range children {
		child := finishChild(t, store, id, domainagent.TaskStatusCompleted, "第"+fmt.Sprint(child2seq(store, id))+"步产出")
		r.OnTaskFinished(child)
	}

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusCompleted, got.Status)
	require.NotNil(t, got.Artifact)
	assert.Contains(t, *got.Artifact, "云南7日行")
	assert.Contains(t, *got.Artifact, "查资料")
	assert.Equal(t, []int64{plan.ID}, gw.notified, "容器终态交给汇报链路,只通知一次")
}

func child2seq(store *fakePlanStore, id int64) int {
	if t, _ := store.GetByID(id); t != nil {
		return t.StepSeq
	}
	return 0
}

func TestPlanRunner_FailurePausesAndNotifies(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	var texts []string
	r.SetProgress(func(_ context.Context, _ *domainagent.AgentTask, text string) { texts = append(texts, text) })

	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	failed := finishChild(t, store, children[0], domainagent.TaskStatusFailed, "")
	r.OnTaskFinished(failed)

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusPaused, got.Status, "失败即暂停")
	assert.Len(t, gw.launched, 1, "暂停后不再派下一步")
	require.Len(t, texts, 1, "失败推一条(成功步骤不推)")
	assert.Contains(t, texts[0], "卡住了")
	assert.Contains(t, texts[0], "1/3")
}

func TestPlanRunner_NoProgressOnSuccess(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	var texts []string
	r.SetProgress(func(_ context.Context, _ *domainagent.AgentTask, text string) { texts = append(texts, text) })

	plan, err := r.CreateAndStart(context.Background(), 42, "P", "g", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]
	r.OnTaskFinished(finishChild(t, store, children[0], domainagent.TaskStatusCompleted, "ok"))
	r.OnTaskFinished(finishChild(t, store, children[1], domainagent.TaskStatusCompleted, "ok"))

	assert.Empty(t, texts, "成功步骤静默(只有失败才推)")
}

func TestPlanRunner_CancelCascades(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	require.NoError(t, r.Cancel(context.Background(), 42, plan.ID))

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusCancelled, got.Status)
	assert.Equal(t, []int64{children[0]}, gw.cancelled, "级联取消当前步")
	assert.Contains(t, gw.notified, plan.ID, "取消也要汇报")
}

func TestPlanRunner_Cancel_NotOwned(t *testing.T) {
	r, _, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "P", "g", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	assert.ErrorIs(t, r.Cancel(context.Background(), 999, plan.ID), ErrPlanNotFound)
	assert.NotEmpty(t, gw.launched)
}

func TestPlanRunner_ResumeRetriesFailedStep(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]
	r.OnTaskFinished(finishChild(t, store, children[0], domainagent.TaskStatusFailed, ""))

	require.NoError(t, r.Resume(context.Background(), 42, plan.ID))

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusRunning, got.Status)
	assert.Equal(t, []int64{children[0]}, gw.reset, "重试失败步")
	assert.Len(t, gw.launched, 2)
	// 已完成计划不可续跑
	for _, id := range children {
		task, _ := store.GetByID(id)
		if task != nil && task.Status != domainagent.TaskStatusCompleted {
			task.Status = domainagent.TaskStatusCompleted
		}
	}
	plan2, _ := store.GetByID(plan.ID)
	plan2.Status = domainagent.TaskStatusCompleted
	assert.ErrorIs(t, r.Resume(context.Background(), 42, plan.ID), ErrPlanNotResumable)
}

func TestPlanRunner_RecoverCompletesWhenAllDone(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "云南7日行", "做行程", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	for _, id := range gw.children[plan.ID] {
		finishChild(t, store, id, domainagent.TaskStatusCompleted, "done")
	}

	r.Recover(context.Background())

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusCompleted, got.Status, "重启恢复:全部完成 → 收尾")
}

func TestPlanRunner_IgnoresNonChildTask(t *testing.T) {
	r, _, gw := newRunnerFixture(t)
	r.OnTaskFinished(&domainagent.AgentTask{ID: 1, Status: domainagent.TaskStatusCompleted})
	assert.Empty(t, gw.launched)
}

func TestPlanRunner_DigestTruncated(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "P", "g", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	long := strings.Repeat("云", domainagent.PlanDigestLimit+20)
	r.OnTaskFinished(finishChild(t, store, children[0], domainagent.TaskStatusCompleted, long))

	second, _ := store.GetByID(children[1])
	prev, _ := second.TaskSpec.Background["上一步结果"].(string)
	assert.True(t, strings.HasSuffix(prev, domainagent.PlanDigestTruncatedMark))
}

// 超时类失败自动重试一次(用户反馈:一次 180s 超时整单停摆太脆)。
func TestPlanRunner_RetryOnceOnTimeout(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "西安自驾", "做攻略", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	// 第一步因超时失败(Attempts=0)
	store.mu.Lock()
	child := store.tasks[children[0]]
	child.Status = domainagent.TaskStatusFailed
	msg := "子 Agent 执行超时"
	child.ErrorMsg = &msg
	store.mu.Unlock()
	r.OnTaskFinished(child)

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusRunning, got.Status, "超时自动重试,不暂停")
	assert.Equal(t, []int64{children[0]}, gw.reset, "重置失败步")
	assert.Len(t, gw.launched, 2, "重新派同一步")

	// 重试后仍超时(Attempts=1)→ 暂停,不再无限重试
	store.mu.Lock()
	child2 := store.tasks[children[0]]
	child2.Status = domainagent.TaskStatusFailed
	child2.Attempts = 1
	child2.ErrorMsg = &msg
	store.mu.Unlock()
	r.OnTaskFinished(child2)

	got2, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusPaused, got2.Status, "只自动重试一次")
}

// 业务性失败(工具明确报错)不自动重试,直接暂停等用户判断。
func TestPlanRunner_NoRetryOnBusinessFailure(t *testing.T) {
	r, store, gw := newRunnerFixture(t)
	plan, err := r.CreateAndStart(context.Background(), 42, "P", "g", planSteps(), domainagent.SourceWeb, "", 0)
	require.NoError(t, err)
	children := gw.children[plan.ID]

	store.mu.Lock()
	child := store.tasks[children[0]]
	child.Status = domainagent.TaskStatusFailed
	msg := "工具调用失败"
	child.ErrorMsg = &msg
	store.mu.Unlock()
	r.OnTaskFinished(child)

	got, _ := store.GetByID(plan.ID)
	assert.Equal(t, domainagent.TaskStatusPaused, got.Status)
	assert.Empty(t, gw.reset, "业务失败不重试")
}
