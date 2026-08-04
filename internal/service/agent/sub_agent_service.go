package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	conversation "omnibot/internal/domain/conversation"
	repoagent "omnibot/internal/repository/agent"
	chatrepo "omnibot/internal/repository/chat"
	"omnibot/pkg/logger"
)

// SubAgentRunner 子 Agent 执行器接口:按 card 的 prompt+工具集跑一次 Agent,返回最终产出。
// 生产实现见 subAgentRunnerImpl(用 ReActAgent + 系统默认 LLM);测试可 mock。
type SubAgentRunner interface {
	// Run 按 card 配置执行子 Agent。taskSpec 任务包(含 goal+背景+交付物+完成标准)注入 system prompt。
	// userID 用于查用户 LLM 配置(方案3:优先用户配置,无则系统默认)。
	// taskID 用于装配 NoteInjectionHook(running 态 update_task 追加的 notes 注入子 Agent 上下文)。
	// onStep:每产生一步(LLM调用/工具调用)立即回调,供上层实时落 agent_steps--
	// 任务 running 中即可观测执行过程,而非等结束批量落。nil 时跳过回调(测试可用)。
	// 返回子 Agent 的最终回复(FinalResponse)作为 Artifact。
	Run(ctx context.Context, taskID, userID int64, card domainagent.SubAgentCard, taskSpec domainagent.TaskSpec, onStep func(StepRecord)) (artifact string, err error)
}

// SubAgentService 后台子 Agent 任务服务(08 技术方案 §4.3)。
//
// StartTask 建任务 + 起 goroutine 后台执行(executeTask),立即返回 task_id(异步);
// executeTask 调 SubAgentRunner 跑子 Agent,写 Artifact,更新状态;
// GetCompletedUnreported 供主 Agent 前置汇报查询;MarkReported 汇报后标记。
type SubAgentService struct {
	taskRepo     repoagent.AgentTaskRepository
	registry     *SubAgentRegistry
	runner       SubAgentRunner
	stepRepo     chatrepo.AgentStepRepository     // 子 Agent 运行链路落 agent_steps(方案A,task_id 关联)
	artifactRepo repoagent.ArtifactRepository     // 子 Agent 产物落 agent_artifacts(结构化 Artifact,#18)
	eventRepo    repoagent.TaskEventRepository    // 任务事件流落 agent_task_events(#22)
	notifier     TaskNotifier                      // 任务完成主动推送(方案A:飞书主动消息)

	// activeCancels 记录 running 任务的 cancel 函数,供 CancelTask 触发 ctx 取消。
	// key=taskID。executeTask 启动注册,结束(成功/失败/panic)注销。mutex 保护并发。
	cancelMu      sync.Mutex
	activeCancels map[int64]context.CancelFunc
	// eventSeq 记录每任务的下一个事件序号(任务内递增,幂等用)。
	eventSeqMu sync.Mutex
	eventSeq   map[int64]int
}

// NewSubAgentService 创建子 Agent 服务。
// artifactRepo/eventRepo/notifier 可为 nil(此时不落结构化产物/事件/不主动推送,兼容老路径)。
func NewSubAgentService(
	taskRepo repoagent.AgentTaskRepository,
	registry *SubAgentRegistry,
	runner SubAgentRunner,
	stepRepo chatrepo.AgentStepRepository,
	artifactRepo repoagent.ArtifactRepository,
	eventRepo repoagent.TaskEventRepository,
	notifier TaskNotifier,
) *SubAgentService {
	return &SubAgentService{
		taskRepo:       taskRepo,
		registry:       registry,
		runner:         runner,
		stepRepo:       stepRepo,
		artifactRepo:   artifactRepo,
		eventRepo:      eventRepo,
		notifier:       notifier,
		activeCancels:  make(map[int64]context.CancelFunc),
		eventSeq:       make(map[int64]int),
	}
}

// StartTask 建任务 + 起 goroutine 后台执行,立即返回 task_id(异步,不阻塞调用方)。
// subAgentType 未注册返回 error(早失败,不静默)。taskSpec 为任务包(含 goal+背景+交付物+完成标准)。
// source 来源渠道(web/feishu);notifyTarget 主动推送目标(feishu=open_id)。
func (s *SubAgentService) StartTask(ctx context.Context, userID int64, subAgentType string, taskSpec domainagent.TaskSpec, source, notifyTarget string) (int64, error) {
	if _, ok := s.registry.Get(subAgentType); !ok {
		return 0, fmt.Errorf("sub agent %q not registered", subAgentType)
	}

	task := domainagent.NewAgentTask(userID, subAgentType, taskSpec, source, notifyTarget)
	if err := s.taskRepo.Create(task); err != nil {
		return 0, fmt.Errorf("create agent task: %w", err)
	}
	s.recordEvent(task.ID, domainagent.EventTaskSubmitted, "main")

	// 后台执行。用独立 context(不继承请求 ctx,请求结束子 Agent 继续跑)。
	go s.executeTask(context.Background(), task)
	return task.ID, nil
}

// executeTask 跑子 Agent,写 Artifact,更新状态。goroutine 内执行,panic 兜底。
func (s *SubAgentService) executeTask(_ context.Context, task *domainagent.AgentTask) {
	// 注销 activeCancels(无论成功/失败/panic/cancel 都要清理)
	defer s.unregisterCancel(task.ID)
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorWithFields("sub agent: panic in executeTask",
				zap.Int64("task_id", task.ID),
				zap.Any("recover", r),
			)
			errMsg := fmt.Sprintf("内部错误: %v", r)
			_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		}
	}()

	card, ok := s.registry.Get(task.SubAgentType)
	if !ok {
		errMsg := fmt.Sprintf("子 Agent %q 未注册", task.SubAgentType)
		_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		return
	}

	// 启动前检查:若已被 cancel(pending 态被取消),直接置 cancelled 跳过执行。
	cur, err := s.taskRepo.GetByID(task.ID)
	if err == nil && cur.Status == domainagent.TaskStatusCancelled {
		return
	}

	// running
	if err := s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil); err != nil {
		logger.ErrorWithFields("sub agent: update to running failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return
	}
	s.recordEvent(task.ID, domainagent.EventTaskRunning, "sub")

	// 带 card.Timeout 执行,且 ctx 可被外部 cancel(CancelTask 触发)。
	ctx, cancel := context.WithTimeout(context.Background(), card.Timeout)
	defer cancel()
	s.registerCancel(task.ID, cancel)

	// onStep:每产生一步(LLM调用/工具调用)立即落 agent_steps(task_id 关联,自增 seq)。
	// 使任务 running 中即可查到执行过程,而非等结束批量落。落库失败仅记日志,不阻断执行。
	seq := 0
	onStep := func(r StepRecord) {
		if s.stepRepo == nil {
			return
		}
		steps := StepRecordsToAgentSteps([]StepRecord{r}, task.UserID, "")
		if len(steps) == 0 {
			return
		}
		step := steps[0]
		step.Seq = seq
		seq++
		taskID := task.ID
		step.TaskID = &taskID
		if err := s.stepRepo.CreateBatch([]*conversation.AgentStep{step}); err != nil {
			logger.ErrorWithFields("sub agent: save step failed",
				zap.Int64("task_id", task.ID),
				zap.Int("seq", step.Seq),
				zap.Error(err))
		}
	}

	artifact, runErr := s.runner.Run(ctx, task.ID, task.UserID, card, task.TaskSpec, onStep)

	// 识别取消 vs 真失败:若 ctx 被 cancel(外部 CancelTask 触发),置 cancelled 而非 failed。
	if ctx.Err() == context.Canceled {
		_ = s.taskRepo.Cancel(task.ID)
		s.recordEvent(task.ID, domainagent.EventTaskCancelled, "main")
		s.notifyCompleted(task.ID)
		logger.InfoWithFields("sub agent: task cancelled",
			zap.Int64("task_id", task.ID),
			zap.String("sub_agent", task.SubAgentType))
		return
	}
	if runErr != nil {
		errMsg := sanitizeSubAgentError(runErr)
		logger.ErrorWithFields("sub agent: run failed",
			zap.Int64("task_id", task.ID),
			zap.String("sub_agent", task.SubAgentType),
			zap.Error(runErr),
		)
		_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		s.recordEvent(task.ID, domainagent.EventTaskFailed, "sub")
		s.notifyCompleted(task.ID)
		return // 步骤已随 onStep 实时落库,无需再批量落
	}

	if strings.TrimSpace(artifact) == "" {
		errMsg := "子 Agent 未产出有效结果"
		_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		s.recordEvent(task.ID, domainagent.EventTaskFailed, "sub")
		s.notifyCompleted(task.ID)
		return
	}

	// 子 Agent 调 request_input 后任务已置 input_required(RequestInput 改了状态)。
	// 此时 runner.Run 返回(本轮结束),不要覆盖成 completed--任务挂起等输入。
	cur2, _ := s.taskRepo.GetByID(task.ID)
	if cur2 != nil && cur2.Status == domainagent.TaskStatusInputRequired {
		s.recordEvent(task.ID, domainagent.EventTaskInputRequired, "sub")
		logger.InfoWithFields("sub agent: task suspended for input",
			zap.Int64("task_id", task.ID))
		return
	}

	if err := s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, &artifact, nil); err != nil {
		logger.ErrorWithFields("sub agent: update to completed failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
	}
	s.recordEvent(task.ID, domainagent.EventTaskCompleted, "sub")
	// 落结构化 artifact(独立表,#18)。子 Agent 产出当前是自由文本,包装为 markdown artifact。
	// task.Artifact 仍存文本(向后兼容)。artifactRepo 为 nil 时跳过(老路径)。
	if s.artifactRepo != nil {
		art := domainagent.NewMarkdownArtifact(task.ID, "result", artifact)
		if err := s.artifactRepo.Create(art); err != nil {
			logger.ErrorWithFields("sub agent: save artifact failed",
				zap.Int64("task_id", task.ID), zap.Error(err))
		}
	}
	// 完成推送(飞书主动消息):artifact 已落库,推送摘要。web 任务跳过(靠轮询)。
	s.notifyCompleted(task.ID)
}

// GetCompletedUnreported 返回该用户已完成但未汇报的任务(含 failed,失败也要汇报)。
func (s *SubAgentService) GetCompletedUnreported(userID int64) ([]*domainagent.AgentTask, error) {
	return s.taskRepo.ListCompletedUnreported(userID)
}

// MarkReported 标记任务已汇报。
func (s *SubAgentService) MarkReported(taskID int64) error {
	return s.taskRepo.MarkReported(taskID)
}

// GetTask 取单个任务(供 report 接口用)。
func (s *SubAgentService) GetTask(taskID int64) (*domainagent.AgentTask, error) {
	return s.taskRepo.GetByID(taskID)
}

// GetTaskArtifact 取任务的结构化产物(#18)。无产物或 artifactRepo 未装配返回 nil。
// 主 Agent 据此按 schema 字段取用,而非解析 task.Artifact 自由文本。
func (s *SubAgentService) GetTaskArtifact(taskID int64) (*domainagent.Artifact, error) {
	if s.artifactRepo == nil {
		return nil, nil
	}
	art, err := s.artifactRepo.GetByTaskID(taskID)
	if err != nil {
		return nil, nil // 无产物返回 nil(不报错,调用方按 nil 处理)
	}
	return art, nil
}

// TaskSummary 任务概要(供 query_task 工具返回给 LLM)。精简,避免 token 爆炸:只给状态/goal 摘要/步骤数。
type TaskSummary struct {
	ID         int64   `json:"id"`
	UserID     int64   `json:"-"`
	SubAgent   string  `json:"sub_agent"`
	Goal       string  `json:"goal"`
	Status     string  `json:"status"`
	StepCount  int     `json:"step_count"`
	Reported   bool    `json:"reported"`
	Artifact   *string `json:"artifact,omitempty"` // completed 时给摘要
}

// QueryTask 查单个任务概要。属主校验:只能查自己的任务。
func (s *SubAgentService) QueryTask(userID, taskID int64) (*TaskSummary, error) {
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil {
		return nil, err
	}
	if task.UserID != userID {
		return nil, ErrTaskNotOwned
	}
	return s.toSummary(task)
}

// ListUserTasks 列出该用户最近的任务概要(倒序)。供 query_task 工具无 task_id 时调用。
func (s *SubAgentService) ListUserTasks(userID int64, limit int) ([]*TaskSummary, error) {
	tasks, err := s.taskRepo.ListByUser(userID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]*TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		sm, err := s.toSummary(t)
		if err != nil {
			continue
		}
		out = append(out, sm)
	}
	return out, nil
}

// toSummary 把 AgentTask 转概要,含步骤数(从 stepRepo 查)。
func (s *SubAgentService) toSummary(task *domainagent.AgentTask) (*TaskSummary, error) {
	sm := &TaskSummary{
		ID: task.ID, UserID: task.UserID, SubAgent: task.SubAgentType,
		Goal: task.Goal, Status: task.Status, Reported: task.Reported, Artifact: task.Artifact,
	}
	if s.stepRepo != nil {
		steps, err := s.stepRepo.ListByTaskID(task.ID)
		if err == nil {
			sm.StepCount = len(steps)
		}
	}
	return sm, nil
}

// CancelTask 取消任务。pending/running 可取消;已结束(completed/failed/cancelled)拒绝。
// running 态:触发 activeCancels[id]() 让 runner ctx 取消,executeTask 识别后置 cancelled。
// pending 态:直接 repo.Cancel(executeTask 启动时检查状态会跳过)。
func (s *SubAgentService) CancelTask(userID, taskID int64) error {
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil {
		return err
	}
	if task.UserID != userID {
		return ErrTaskNotOwned
	}
	if task.IsTerminal() {
		return fmt.Errorf("任务已结束(状态:%s),不可取消", task.Status)
	}
	// running:触发 ctx 取消,executeTask 会识别 ctx.Canceled 置 cancelled
	s.cancelMu.Lock()
	cancelFn, ok := s.activeCancels[taskID]
	s.cancelMu.Unlock()
	if ok && task.Status == domainagent.TaskStatusRunning {
		cancelFn()
		return nil
	}
	// pending(还没起 runner):直接置 cancelled,executeTask 启动时检查会跳过
	if err := s.taskRepo.Cancel(taskID); err != nil {
		return err
	}
	s.recordEvent(taskID, domainagent.EventTaskCancelled, "main")
	return nil
}

// RequestInput 子 Agent 主动要输入(#19):把任务置 input_required + 把问题存 Notes。
// 子 Agent 调 request_input 工具时触发。任务挂起(子 Agent goroutine 本轮结束),
// 主 Agent query 看到 input_required + 问题(读 Notes),问用户后用 UpdateTask 补答案。
// 续跑靠主 Agent 重新 delegate 关联 parent_task_id 的新任务(不自动恢复,见 10-规划 §3)。
func (s *SubAgentService) RequestInput(taskID int64, question string) error {
	if err := s.taskRepo.AppendNote(taskID, "[需要输入] "+question); err != nil {
		return fmt.Errorf("append note: %w", err)
	}
	if err := s.taskRepo.UpdateStatus(taskID, domainagent.TaskStatusInputRequired, nil, nil); err != nil {
		return err
	}
	s.recordEvent(taskID, domainagent.EventTaskInputRequired, "sub")
	return nil
}

// UpdateTask 更新/补充任务信息。按状态分:
//   - pending:改 goal(任务还没跑,直接更新)
//   - running:追加 notes(补充信息,子 Agent 下轮经 NoteInjectionHook 注入上下文)
//   - 已结束:拒绝
// goal 空串不改;note 空串不追加(按需调用)。
func (s *SubAgentService) UpdateTask(userID, taskID int64, goal, note string) error {
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil {
		return err
	}
	if task.UserID != userID {
		return ErrTaskNotOwned
	}
	if task.IsTerminal() {
		return fmt.Errorf("任务已结束(状态:%s),不可更新", task.Status)
	}
	switch task.Status {
	case domainagent.TaskStatusPending:
		if strings.TrimSpace(goal) != "" {
			if err := s.taskRepo.UpdateGoal(taskID, goal); err != nil {
				return fmt.Errorf("update goal: %w", err)
			}
		}
		if strings.TrimSpace(note) != "" {
			if err := s.taskRepo.AppendNote(taskID, note); err != nil {
				return fmt.Errorf("append note: %w", err)
			}
		}
	case domainagent.TaskStatusRunning, domainagent.TaskStatusInputRequired:
		// running 和 input_required 都可补 note(NoteInjectionHook 下轮读到)。
		// input_required 补后状态保持(不自动回 running--子 Agent goroutine 已挂起,
		// 续跑靠主 Agent 重新 delegate 关联 parent_task_id 的新任务,见 10-规划 §3)。
		if strings.TrimSpace(goal) != "" {
			return fmt.Errorf("运行中任务不可改 goal,请用 note 补充信息")
		}
		if strings.TrimSpace(note) != "" {
			if err := s.taskRepo.AppendNote(taskID, note); err != nil {
				return fmt.Errorf("append note: %w", err)
			}
		}
	}
	return nil
}

// registerCancel 注册 running 任务的 cancel 函数(供 CancelTask 触发)。
func (s *SubAgentService) registerCancel(taskID int64, cancel context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.activeCancels[taskID] = cancel
}

// unregisterCancel 注销 cancel 函数(executeTask 结束时调,无论成功/失败/cancel)。
func (s *SubAgentService) unregisterCancel(taskID int64) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	delete(s.activeCancels, taskID)
}

// recordEvent 记录任务状态变更事件(#22)。eventRepo 为 nil 时跳过。
// sequence 按任务内递增(幂等用)。source 标来源(main/sub)。
func (s *SubAgentService) recordEvent(taskID int64, eventType, source string) {
	if s.eventRepo == nil {
		return
	}
	s.eventSeqMu.Lock()
	s.eventSeq[taskID]++
	seq := s.eventSeq[taskID]
	s.eventSeqMu.Unlock()
	ev := domainagent.NewTaskEvent(taskID, eventType, seq, source)
	if err := s.eventRepo.Create(&ev); err != nil {
		logger.ErrorWithFields("sub agent: record event failed",
			zap.Int64("task_id", taskID), zap.String("event", eventType), zap.Error(err))
	}
}

// notifyCompleted 任务终态时若来自飞书(source=feishu + notifyTarget=open_id),主动推送汇报。
// 推送后标记 reported(防前置汇报重复)。notifier 为 nil 或 source 非 feishu 时跳过(靠轮询/前置汇报)。
// 推送失败仅记日志,不回滚任务状态(任务已完成是事实,推送是尽力而为)。
func (s *SubAgentService) notifyCompleted(taskID int64) {
	if s.notifier == nil {
		return
	}
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil || task == nil {
		return
	}
	if task.Source != domainagent.SourceFeishu || task.NotifyTarget == "" {
		return // web 任务靠轮询 + 前置汇报;非飞书不主动推
	}
	if err := s.notifier.NotifyTaskCompleted(context.Background(), task.NotifyTarget, task); err != nil {
		logger.ErrorWithFields("sub agent: notify feishu failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", task.NotifyTarget), zap.Error(err))
		return
	}
	// 推送成功标记 reported(防下次对话前置汇报重复推)
	if err := s.taskRepo.MarkReported(task.ID); err != nil {
		logger.ErrorWithFields("sub agent: mark reported after notify failed",
			zap.Int64("task.ID", task.ID), zap.Error(err))
	}
}

// ErrTaskNotOwned 任务不属于该用户(属主校验失败)。
var ErrTaskNotOwned = errors.New("task not owned by user")

// SetNotifier 注入任务完成推送器(飞书 channel 启动后调,因 sender 在那时才创建)。
// 飞书未配置时保持 nil(web 任务靠轮询,不主动推)。
func (s *SubAgentService) SetNotifier(n TaskNotifier) {
	s.notifier = n
}

// ListTaskSteps 返回某子 Agent 任务的执行步骤链(LLM调用 + 工具调用),按 seq 正序还原时序。
// 供排查/展示子 Agent 执行过程(可观测性)。属主校验:只能查自己的任务(安全红线),
// 否则返回 ErrTaskNotOwned。
func (s *SubAgentService) ListTaskSteps(taskID, userID int64) ([]*conversation.AgentStep, error) {
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil {
		return nil, err
	}
	if task.UserID != userID {
		return nil, ErrTaskNotOwned
	}
	return s.stepRepo.ListByTaskID(taskID)
}

// GetPendingReportContext 返回待汇报的回执指令 + 对应任务 ID(供主对话前置汇报兜底)。
// 封装:查未汇报任务 + 构造回执指令。无待汇报任务返回空 instruction + nil taskIDs。
// web/飞书主对话 handler 在调主 Agent Run 前调用此方法,有指令则 prepend 到上下文。
func (s *SubAgentService) GetPendingReportContext(userID int64) (instruction string, taskIDs []int64) {
	unreported, err := s.taskRepo.ListCompletedUnreported(userID)
	if err != nil || len(unreported) == 0 {
		return "", nil
	}
	instruction = BuildReportInstruction(s.registry, unreported, false)
	for _, t := range unreported {
		taskIDs = append(taskIDs, t.ID)
	}
	return instruction, taskIDs
}

// sanitizeSubAgentError 把子 Agent 错误转成不泄露内部细节的友好文案(安全红线)。
// 超时/达最大步数有明确文案;其他统一"执行失败"。
func sanitizeSubAgentError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "timeout") {
		return "子 Agent 执行超时"
	}
	if strings.Contains(msg, "最大步数") || strings.Contains(msg, "max steps") {
		return "子 Agent 达到最大步数限制"
	}
	return "子 Agent 执行失败"
}

// compile-time: 保证 sync 被使用(预留并发控制扩展点)
var _ = sync.Mutex{}
var _ = time.Second
