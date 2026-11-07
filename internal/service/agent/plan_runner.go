package agent

// plan_runner.go — 计划推进器(34 号,v2.12 合并建模)。
//
// 建模:计划就是一条 kind=plan 的 AgentTask,步骤是它的子任务(ParentTaskID + StepSeq)。
// 推进器只做三件事:
//   1. 派下一步(把上一步的结论摘要注入子任务背景)
//   2. 某步失败 → 容器 paused(等用户决定)
//   3. 全部完成 → 容器 completed + artifact 汇总,交给普通任务汇报链路
//
// 并发/idempotent 靠既有 CAS:子任务 pending→running 由 executeTask CAS,
// 容器状态迁移由 TransitionStatus CAS——重复回调不会跑两步。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/pkg/logger"
)

// PlanTaskStore 推进器需要的任务读写窄接口(AgentTaskRepository 满足)。
type PlanTaskStore interface {
	GetByID(id int64) (*domainagent.AgentTask, error)
	TransitionStatus(id int64, from, to string, artifact *string, errorMsg *string) (bool, error)
	UpdateTaskSpec(id int64, spec domainagent.TaskSpec) error
	ListRootByStatus(status string) ([]*domainagent.AgentTask, error)
	ListByUser(userID int64, limit int) ([]*domainagent.AgentTask, error)
}

// PlanTaskGateway 与任务执行框架之间的窄接口(SubAgentService 实现)。
type PlanTaskGateway interface {
	CreatePlanTree(ctx context.Context, userID int64, name, goal string, steps []domainagent.PlanStepSpec, source, notifyTarget string, originTurnID int64) (*domainagent.AgentTask, error)
	LaunchChild(ctx context.Context, childID int64) error
	ChildrenOf(planID int64) ([]*domainagent.AgentTask, error)
	ResetChildForRetry(childID int64) error
	CancelTask(userID, taskID int64) error
	// NotifyFinished 容器终态后补发汇报(计划不经 executeTask,需要手动收尾)。
	NotifyFinished(taskID int64)
}

// PlanCreator 派活工具建计划的窄接口(PlanRunner 实现)。
type PlanCreator interface {
	CreateAndStart(ctx context.Context, userID int64, name, goal string, steps []domainagent.PlanStepSpec, source, notifyTarget string, originTurnID int64) (*domainagent.AgentTask, error)
}

// PlanProgressFunc 计划异常推送(34 号:只有失败/暂停才推,成功步骤静默)。
type PlanProgressFunc func(ctx context.Context, plan *domainagent.AgentTask, text string)

// 计划操作错误语义。
var (
	ErrPlanNotFound     = errors.New("plan not found")
	ErrPlanNotResumable = errors.New("plan not resumable")
	ErrPlanNotActive    = errors.New("plan not active")
)

// PlanRunner 计划推进器。
type PlanRunner struct {
	tasks    PlanTaskStore
	gateway  PlanTaskGateway
	progress PlanProgressFunc
	now      func() time.Time
}

// NewPlanRunner 创建推进器(progress 可为 nil,此时不推送异常)。
func NewPlanRunner(tasks PlanTaskStore, gateway PlanTaskGateway) *PlanRunner {
	return &PlanRunner{tasks: tasks, gateway: gateway, now: time.Now}
}

// SetProgress 注入异常推送(装配层在渠道就绪后调用)。
func (r *PlanRunner) SetProgress(fn PlanProgressFunc) { r.progress = fn }

// CreateAndStart 建计划树并派第一步(delegate 带 steps 时走这里)。
// 派发失败时容器已落库并置 paused,编号仍返回给主 Agent。
func (r *PlanRunner) CreateAndStart(ctx context.Context, userID int64, name, goal string, steps []domainagent.PlanStepSpec, source, notifyTarget string, originTurnID int64) (*domainagent.AgentTask, error) {
	if err := domainagent.ValidatePlanSteps(steps); err != nil {
		return nil, err
	}
	plan, err := r.gateway.CreatePlanTree(ctx, userID, name, goal, steps, source, notifyTarget, originTurnID)
	if err != nil {
		return nil, err
	}
	if err := r.Start(ctx, plan); err != nil {
		logger.ErrorWithFields("plan: start after create failed",
			zap.Int64("plan_id", plan.ID), zap.Error(err))
	}
	return plan, nil
}

// Start 启动计划:pending → running,派第 1 步。
func (r *PlanRunner) Start(ctx context.Context, plan *domainagent.AgentTask) error {
	if plan.Kind != domainagent.TaskKindPlan {
		return fmt.Errorf("plan: task %d is not a plan", plan.ID)
	}
	ok, err := r.tasks.TransitionStatus(plan.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlanNotActive
	}
	return r.launchNext(ctx, plan.ID)
}

// OnTaskFinished 子任务终态回调(observer 多播之一)。非子任务直接返回。
func (r *PlanRunner) OnTaskFinished(child *domainagent.AgentTask) {
	if child == nil || child.ParentTaskID == nil {
		return
	}
	plan, err := r.tasks.GetByID(*child.ParentTaskID)
	if err != nil || plan == nil || !plan.IsPlan() {
		return
	}
	if plan.IsTerminal() || plan.Status == domainagent.TaskStatusPaused {
		return // 已收尾/已暂停:等用户决定,不再自动推进
	}

	ctx := context.Background()
	switch child.Status {
	case domainagent.TaskStatusCompleted:
		if err := r.launchNext(ctx, plan.ID); err != nil {
			logger.ErrorWithFields("plan: advance failed",
				zap.Int64("plan_id", plan.ID), zap.Error(err))
		}
	case domainagent.TaskStatusFailed:
		// 超时类"环境性失败"自动重试一次:重新派同一步,不打断整单。
		// 只重试一次(Attempts),业务性失败不重试——避免掩盖真问题。
		if child.Attempts == 0 && isRetryablePlanFailure(child.ErrorMsg) {
			if err := r.gateway.ResetChildForRetry(child.ID); err == nil {
				logger.WarnWithFields("plan: transient failure, retrying step once",
					zap.Int64("plan_id", plan.ID), zap.Int64("child_id", child.ID))
				if err := r.launchNext(ctx, plan.ID); err == nil {
					return
				}
			}
		}
		ok, err := r.tasks.TransitionStatus(plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusPaused, nil, nil)
		if err != nil || !ok {
			return
		}
		total := 0
		if children, e := r.gateway.ChildrenOf(plan.ID); e == nil {
			total = len(children)
		}
		r.notifyProgress(ctx, plan, BuildPlanPauseText(plan, child, total))
	case domainagent.TaskStatusCancelled:
		if ok, _ := r.tasks.TransitionStatus(plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusCancelled, nil, nil); ok {
			r.gateway.NotifyFinished(plan.ID)
		}
	}
}

// launchNext 找第一个未完成的子任务:已终态则收尾容器,否则补背景并派发。
func (r *PlanRunner) launchNext(ctx context.Context, planID int64) error {
	plan, err := r.tasks.GetByID(planID)
	if err != nil {
		return err
	}
	if plan == nil || plan.IsTerminal() {
		return nil
	}
	children, err := r.gateway.ChildrenOf(planID)
	if err != nil {
		return err
	}
	if len(children) == 0 {
		return fmt.Errorf("plan %d has no children", planID)
	}

	idx := -1
	for i, c := range children {
		if c.Status != domainagent.TaskStatusCompleted {
			idx = i
			break
		}
	}
	if idx == -1 {
		return r.completePlan(plan, children)
	}
	next := children[idx]
	if next.Status != domainagent.TaskStatusPending {
		return nil // 正在跑/已失败暂停:不需要再派
	}

	prevArtifact := ""
	if idx > 0 && children[idx-1].Artifact != nil {
		prevArtifact = *children[idx-1].Artifact
	}
	step := domainagent.PlanStepSpec{
		Name:               next.Name,
		Goal:               next.Goal,
		Deliverables:       next.TaskSpec.Deliverables,
		CompletionCriteria: next.TaskSpec.CompletionCriteria,
	}
	spec := domainagent.PlanStepSpecFor(plan, step, next.StepSeq, len(children), prevArtifact)
	if err := r.tasks.UpdateTaskSpec(next.ID, spec); err != nil {
		return fmt.Errorf("plan: update step spec: %w", err)
	}
	return r.gateway.LaunchChild(ctx, next.ID)
}

// completePlan 全部步骤完成:写汇总 artifact,容器终态交给普通汇报链路。
func (r *PlanRunner) completePlan(plan *domainagent.AgentTask, children []*domainagent.AgentTask) error {
	summary := BuildPlanResultText(plan, children)
	ok, err := r.tasks.TransitionStatus(plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusCompleted, &summary, nil)
	if err != nil {
		return err
	}
	if !ok {
		return nil // 并发已收尾
	}
	// 容器终态:走既有汇报链路(Web /report、IM 转述),这是 34 号"Web 没有结论"的修复点。
	r.gateway.NotifyFinished(plan.ID)
	return nil
}

// Cancel 取消整单计划(级联取消当前步任务)。
func (r *PlanRunner) Cancel(ctx context.Context, userID, planID int64) error {
	plan, err := r.tasks.GetByID(planID)
	if err != nil {
		return err
	}
	if plan == nil || plan.UserID != userID || !plan.IsPlan() {
		return ErrPlanNotFound
	}
	if plan.IsTerminal() {
		return ErrPlanNotActive
	}
	var activeChild *domainagent.AgentTask
	if children, e := r.gateway.ChildrenOf(planID); e == nil {
		for _, c := range children {
			if c.IsActive() {
				activeChild = c
				break
			}
		}
	}
	ok, err := r.tasks.TransitionStatus(planID, plan.Status, domainagent.TaskStatusCancelled, nil, nil)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlanNotActive
	}
	if activeChild != nil {
		if e := r.gateway.CancelTask(userID, activeChild.ID); e != nil {
			logger.WarnWithFields("plan: cancel active step failed",
				zap.Int64("plan_id", planID), zap.Int64("child_id", activeChild.ID), zap.Error(e))
		}
	}
	r.gateway.NotifyFinished(planID)
	return nil
}

// Resume 续跑暂停的计划:重试当前失败步(不跳过)。
func (r *PlanRunner) Resume(ctx context.Context, userID, planID int64) error {
	plan, err := r.tasks.GetByID(planID)
	if err != nil {
		return err
	}
	if plan == nil || plan.UserID != userID || !plan.IsPlan() {
		return ErrPlanNotFound
	}
	if plan.Status != domainagent.TaskStatusPaused {
		return ErrPlanNotResumable
	}
	ok, err := r.tasks.TransitionStatus(planID, domainagent.TaskStatusPaused, domainagent.TaskStatusRunning, nil, nil)
	if err != nil {
		return err
	}
	if !ok {
		return ErrPlanNotResumable
	}
	// 当前失败步重置为 pending,再走正常推进(重试该步)
	if children, e := r.gateway.ChildrenOf(planID); e == nil {
		for _, c := range children {
			if c.Status == domainagent.TaskStatusFailed {
				if e := r.gateway.ResetChildForRetry(c.ID); e != nil {
					logger.ErrorWithFields("plan: reset failed step failed",
						zap.Int64("plan_id", planID), zap.Int64("child_id", c.ID), zap.Error(e))
				}
				break
			}
		}
	}
	return r.launchNext(ctx, planID)
}

// Recover 重启恢复:running 容器按子任务现状补推进/暂停。
func (r *PlanRunner) Recover(ctx context.Context) {
	plans, err := r.tasks.ListRootByStatus(domainagent.TaskStatusRunning)
	if err != nil {
		logger.ErrorWithFields("plan: recover list failed", zap.Error(err))
		return
	}
	for _, plan := range plans {
		if !plan.IsPlan() {
			continue
		}
		children, err := r.gateway.ChildrenOf(plan.ID)
		if err != nil || len(children) == 0 {
			_ = r.pauseForRecover(plan, "步骤丢失")
			continue
		}
		running := false
		for _, c := range children {
			if c.Status == domainagent.TaskStatusRunning || c.Status == domainagent.TaskStatusInputRequired {
				running = true
				break
			}
		}
		if running {
			continue // 还在跑,等它自己的终态回调
		}
		if err := r.launchNext(ctx, plan.ID); err != nil {
			logger.ErrorWithFields("plan: recover advance failed",
				zap.Int64("plan_id", plan.ID), zap.Error(err))
		}
	}
}

// pauseForRecover 恢复时发现异常 → 暂停,不猜着跑。
func (r *PlanRunner) pauseForRecover(plan *domainagent.AgentTask, reason string) error {
	ok, err := r.tasks.TransitionStatus(plan.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusPaused, nil, nil)
	if err != nil || !ok {
		return err
	}
	logger.WarnWithFields("plan: paused on recover",
		zap.Int64("plan_id", plan.ID), zap.String("reason", reason))
	return nil
}

// ListByUser 列出用户的计划(manage_plan status 不带 plan_id 时用)。
func (r *PlanRunner) ListByUser(userID int64, limit int) ([]*domainagent.AgentTask, error) {
	roots, err := r.tasks.ListByUser(userID, limit)
	if err != nil {
		return nil, err
	}
	plans := make([]*domainagent.AgentTask, 0, len(roots))
	for _, t := range roots {
		if t.IsPlan() {
			plans = append(plans, t)
		}
	}
	return plans, nil
}

// Status 查计划 + 子任务(属主不符按不存在处理)。
func (r *PlanRunner) Status(userID, planID int64) (*domainagent.AgentTask, []*domainagent.AgentTask, error) {
	plan, err := r.tasks.GetByID(planID)
	if err != nil {
		return nil, nil, err
	}
	if plan == nil || plan.UserID != userID || !plan.IsPlan() {
		return nil, nil, ErrPlanNotFound
	}
	children, err := r.gateway.ChildrenOf(planID)
	if err != nil {
		return nil, nil, err
	}
	return plan, children, nil
}

// isRetryablePlanFailure 是否值得自动重试一次。
// 只认我们自己的超时文案(sanitizeSubAgentError 产出),不猜供应商错误细节。
func isRetryablePlanFailure(errMsg *string) bool {
	if errMsg == nil {
		return false
	}
	return strings.Contains(*errMsg, "执行超时")
}

// notifyProgress 异常推送(成功步骤不调)。
func (r *PlanRunner) notifyProgress(ctx context.Context, plan *domainagent.AgentTask, text string) {
	if r.progress == nil || text == "" {
		return
	}
	r.progress(ctx, plan, text)
}
