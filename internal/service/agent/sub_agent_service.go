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
	// Run 执行子 Agent(通用执行器,去角色后无 card)。taskSpec 任务包(含 goal+背景+交付物+完成标准+
	// persona_hint)注入 system prompt。userID 用于查用户 LLM 配置(方案3:优先用户配置,无则系统默认)。
	// taskID 用于装配 NoteInjectionHook(running 态 update_task 追加的 notes 注入子 Agent 上下文)。
	// MaxSteps/Timeout 由 taskSpec.Constraints 覆盖或框架默认(不给 LLM 自由填)。
	// onStep:每产生一步(LLM调用/工具调用)立即回调,供上层实时落 agent_steps--
	// 任务 running 中即可观测执行过程,而非等结束批量落。nil 时跳过回调(测试可用)。
	// 返回子 Agent 的最终回复(FinalResponse)作为 Artifact。
	Run(ctx context.Context, taskID, userID int64, taskSpec domainagent.TaskSpec, onStep func(StepRecord)) (artifact string, err error)
}

// SubAgentService 后台子 Agent 任务服务(08 技术方案 §4.3)。
//
// StartTask 建任务 + 起 goroutine 后台执行(executeTask),立即返回 task_id(异步);
// executeTask 调 SubAgentRunner 跑子 Agent,写 Artifact,更新状态;
// GetCompletedUnreported 供主 Agent 前置汇报查询;MarkReported 汇报后标记。
type SubAgentService struct {
	taskRepo     repoagent.AgentTaskRepository
	runner       SubAgentRunner
	stepRepo     chatrepo.AgentStepRepository  // 子 Agent 运行链路落 agent_steps(方案A,task_id 关联)
	artifactRepo repoagent.ArtifactRepository  // 子 Agent 产物落 agent_artifacts(结构化 Artifact,#18)
	eventRepo    repoagent.TaskEventRepository // 任务事件流落 agent_task_events(#22)
	notifier     TaskNotifier                  // 任务完成主动推送(方案A:飞书主动消息)
	publisher    TaskCompletionPublisher       // web 任务完成实时推送(08 §4.8,realtime.Hub 实现)

	// activeCancels 记录 running 任务的 cancel 函数,供 CancelTask 触发 ctx 取消。
	// key=taskID。executeTask 启动注册,结束(成功/失败/panic)注销。mutex 保护并发。
	cancelMu      sync.Mutex
	activeCancels map[int64]context.CancelFunc
}

// NewSubAgentService 创建子 Agent 服务。
// artifactRepo/eventRepo/notifier 可为 nil(此时不落结构化产物/事件/不主动推送,兼容老路径)。
// 去角色后不接收 SubAgentRegistry:子 Agent 是通用执行器,不存在角色卡注册层。
func NewSubAgentService(
	taskRepo repoagent.AgentTaskRepository,
	runner SubAgentRunner,
	stepRepo chatrepo.AgentStepRepository,
	artifactRepo repoagent.ArtifactRepository,
	eventRepo repoagent.TaskEventRepository,
	notifier TaskNotifier,
) *SubAgentService {
	return &SubAgentService{
		taskRepo:      taskRepo,
		runner:        runner,
		stepRepo:      stepRepo,
		artifactRepo:  artifactRepo,
		eventRepo:     eventRepo,
		notifier:      notifier,
		activeCancels: make(map[int64]context.CancelFunc),
	}
}

// StartTask 建任务 + 起 goroutine 后台执行,立即返回 task_id(异步,不阻塞调用方)。
// taskSpec 为任务包(含 goal+背景+交付物+完成标准+persona_hint),其 Type 作溯源标签。
// 去角色后不校验任何注册:任何任务都交给通用执行器。source 来源渠道(web/feishu);notifyTarget 主动推送目标。
// originTurnID 是触发本任务的用户意图 Turn(Phase 1,16-架构迭代路线图 §5.3),0 表示无。
func (s *SubAgentService) StartTask(ctx context.Context, userID int64, taskSpec domainagent.TaskSpec, source, notifyTarget string, originTurnID int64) (int64, error) {
	task := domainagent.NewAgentTask(userID, taskSpec, source, notifyTarget)
	if originTurnID > 0 {
		task.OriginTurnID = &originTurnID
	}
	// Phase 7a(16-路线图 §14):任务与 submitted 事件同一事务落库(eventRepo 为 nil 走老路径)。
	// 事件序号由 task.version 派生(DB 内自洽),不再依赖进程内 eventSeq map。
	if s.eventRepo == nil {
		if err := s.taskRepo.Create(task); err != nil {
			return 0, fmt.Errorf("create agent task: %w", err)
		}
	} else {
		if err := s.taskRepo.CreateWithEvent(task, domainagent.EventTaskSubmitted, "main"); err != nil {
			return 0, fmt.Errorf("create agent task: %w", err)
		}
	}

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
			// CAS 落终态(带事件):running→failed 优先;若 panic 发生在 running 之前(pending),按启动失败落终态
			if ok, _ := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusFailed, nil, &errMsg, domainagent.EventTaskFailed, "sub"); !ok {
				_, _ = s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusFailed, nil, &errMsg, domainagent.EventTaskFailed, "sub")
			}
		}
	}()

	// CAS pending→running(Phase 5)+ 事件同事务(Phase 7a):失败=启动前已被取消(CancelTask 先到),
	// 静默跳过执行。取代旧的"先查再写"两步——两步之间取消会丢失,CAS 一步消除竞态窗口。
	ok, err := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil, domainagent.EventTaskRunning, "sub")
	if err != nil {
		logger.ErrorWithFields("sub agent: transition to running failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return
	}
	if !ok {
		logger.InfoWithFields("sub agent: task cancelled before start, skip execution",
			zap.Int64("task_id", task.ID))
		return
	}

	// ctx 只承担「可被外部 cancel」(CancelTask 触发),不设超时——执行超时由 runner 统一负责
	// (sub_agent_runner.go 的 AgentServiceConfig.Timeout,默认 180s,可配置 agent.sub_agent.timeout)。
	// 单一超时来源,避免 executeTask 与 runner 双超时不一致(此前曾用 DefaultTimeout 120s 误杀耗时研究任务)。
	ctx, cancel := context.WithCancel(context.Background())
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

	artifact, runErr := s.runner.Run(ctx, task.ID, task.UserID, task.TaskSpec, onStep)

	// 识别取消 vs 真失败:若 ctx 被 cancel(外部 CancelTask 触发),CAS 落 cancelled。
	// CancelTask 自己会先 CAS(在触发 cancelFn 之前),所以这里通常 CAS 失败=取消方已收尾,
	// 不重复记事件/推送;仅当 CAS 成功(未来出现非 CancelTask 来源的取消)才补齐收尾。
	if ctx.Err() == context.Canceled {
		if ok, _ := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusCancelled, nil, nil, domainagent.EventTaskCancelled, "main"); ok {
			s.notifyCompleted(task.ID)
		}
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
		// CAS running→failed(带事件):失败=已被并发取消,放弃覆盖(取消路径负责收尾)
		if ok, _ := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusFailed, nil, &errMsg, domainagent.EventTaskFailed, "sub"); ok {
			s.notifyCompleted(task.ID)
		} else {
			logger.InfoWithFields("sub agent: task no longer running, skip failed marking",
				zap.Int64("task_id", task.ID))
		}
		return // 步骤已随 onStep 实时落库,无需再批量落
	}

	if strings.TrimSpace(artifact) == "" {
		errMsg := "子 Agent 未产出有效结果"
		if ok, _ := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusFailed, nil, &errMsg, domainagent.EventTaskFailed, "sub"); ok {
			s.notifyCompleted(task.ID)
		}
		return
	}

	// CAS running→completed(Phase 5):失败=并发状态变化,读回现态分别处理——
	// input_required(request_input 先到,任务挂起不覆盖)/ cancelled(取消先到,不覆盖)。
	// 取代旧的"先查 input_required 再写"两步,消除查询与写入之间的竞态窗口。
	completed, err := s.taskRepo.TransitionStatusWithEvent(task.ID, domainagent.TaskStatusRunning, domainagent.TaskStatusCompleted, &artifact, nil, domainagent.EventTaskCompleted, "sub")
	if err != nil {
		logger.ErrorWithFields("sub agent: transition to completed failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return
	}
	if !completed {
		cur2, _ := s.taskRepo.GetByID(task.ID)
		if cur2 != nil && cur2.Status == domainagent.TaskStatusInputRequired {
			// 不再补记 input_required 事件:迁移本身由 RequestInput 落事件(同事务),
			// 这里补记会与它重复(Phase 7a 前的存量重复)。
			logger.InfoWithFields("sub agent: task suspended for input",
				zap.Int64("task_id", task.ID))
			return
		}
		logger.InfoWithFields("sub agent: task no longer running, skip completed marking",
			zap.Int64("task_id", task.ID),
			zap.String("current_status", func() string {
				if cur2 != nil {
					return cur2.Status
				}
				return "unknown"
			}()))
		return
	}
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

// TaskSummary 任务概要(供 query_task 工具返回给 LLM)。精简,避免 token 爆炸:
// 只给状态/goal 摘要/步骤数 + 关键时间点(创建/开始/结束,LLM 能回答"任务何时派/何时跑完")。
type TaskSummary struct {
	ID        int64      `json:"id"`
	UserID    int64      `json:"-"`
	SubAgent  string     `json:"sub_agent"`
	Name      string     `json:"name,omitempty"` // 任务短名(可空,空则展示层回落 goal 摘要)
	Goal      string     `json:"goal"`
	Status    string     `json:"status"`
	StepCount int        `json:"step_count"`
	Reported  bool       `json:"reported"`
	CreatedAt time.Time  `json:"created_at"`
	StartedAt *time.Time `json:"started_at,omitempty"`
	// FinishedAt 结束时间:completed 取 CompletedAt,cancelled 取 CancelledAt,其余 nil
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	Artifact   *string    `json:"artifact,omitempty"` // completed 时给摘要
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

// toSummary 把 AgentTask 转概要,含步骤数(从 stepRepo 查)与关键时间点。
func (s *SubAgentService) toSummary(task *domainagent.AgentTask) (*TaskSummary, error) {
	sm := &TaskSummary{
		ID: task.ID, UserID: task.UserID, SubAgent: task.SubAgentType,
		Name: task.Name,
		Goal: task.Goal, Status: task.Status, Reported: task.Reported, Artifact: task.Artifact,
		CreatedAt: task.CreatedAt, StartedAt: task.StartedAt,
	}
	if task.Status == domainagent.TaskStatusCancelled && task.CancelledAt != nil {
		sm.FinishedAt = task.CancelledAt
	} else {
		sm.FinishedAt = task.CompletedAt
	}
	if s.stepRepo != nil {
		steps, err := s.stepRepo.ListByTaskID(task.ID)
		if err == nil {
			sm.StepCount = len(steps)
		}
	}
	return sm, nil
}

// CancelTask 取消任务。pending/running/input_required 可取消;已结束(completed/failed/cancelled)拒绝。
// Phase 5 CAS:先 CAS 当前态→cancelled(落定状态 + cancelled_at),成功后再打断 runner——
// 顺序保证取消方拥有收尾权(事件/推送),runner 侧的终态 CAS 必然失败、不会覆盖或重复收尾。
// 旧顺序(先 cancelFn 后写状态)存在"取消指令发出但 executeTask 已写完终态"的竞态窗口。
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
	// CAS 当前态 → cancelled(带事件):失败=读后被并发结束,如实拒绝
	ok, err := s.taskRepo.TransitionStatusWithEvent(taskID, task.Status, domainagent.TaskStatusCancelled, nil, nil, domainagent.EventTaskCancelled, "main")
	if err != nil {
		return err
	}
	if !ok {
		cur, _ := s.taskRepo.GetByID(taskID)
		st := "未知"
		if cur != nil {
			st = cur.Status
		}
		return fmt.Errorf("任务状态已变化(现态:%s),不可取消", st)
	}
	// running:打断 runner(runner 感知 ctx 取消后走收尾,终态已落定,不再写)
	s.cancelMu.Lock()
	cancelFn, hasRunner := s.activeCancels[taskID]
	s.cancelMu.Unlock()
	if hasRunner {
		cancelFn()
	}
	s.notifyCompleted(taskID)
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
	// CAS running→input_required(带事件):失败=并发取消/结束,如实报错(runner 本轮会结束)
	ok, err := s.taskRepo.TransitionStatusWithEvent(taskID, domainagent.TaskStatusRunning, domainagent.TaskStatusInputRequired, nil, nil, domainagent.EventTaskInputRequired, "sub")
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("任务状态已变化,无法挂起等输入")
	}
	return nil
}

// UpdateTask 更新/补充任务信息。按状态分:
//   - pending:改 goal(任务还没跑,直接更新)
//   - running:追加 notes(补充信息,子 Agent 下轮经 NoteInjectionHook 注入上下文)
//   - 已结束:拒绝
//
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

// notifyCompleted 任务终态时若来自飞书(source=feishu + notifyTarget=open_id),主动推送汇报。
//
// 先标记 reported 再推送(顺序关键):飞书任务的汇报由本方法负责(走主 Agent 转述+推送),
// 若在推送(耗时数秒的主 Agent Run)之后才 MarkReported,期间 web 前端轮询会查到
// unreported 触发 HandleReportTask,导致重复汇报(task40:msg202+msg203)。
// 先 MarkReported 让前端轮询查不到该 task,避免重叠。推送失败也保持 reported
// (消息没到是异常,但重复汇报更糟;失败有日志可补救)。
// notifier 为 nil 或 source 非 feishu 时跳过(web 任务靠前端轮询 + 前置汇报)。
func (s *SubAgentService) notifyCompleted(taskID int64) {
	task, err := s.taskRepo.GetByID(taskID)
	if err != nil || task == nil {
		return
	}
	// web:实时推送事件(08 §4.8),前端收到后走 /report 链路拉取汇报。
	// 不标 reported——reported 由 /report 处理,推送丢失时轮询兜底仍可发现。
	if task.Source == domainagent.SourceWeb && s.publisher != nil {
		s.publisher.PublishTaskCompleted(task.UserID, task.ID)
	}
	if s.notifier == nil {
		return
	}
	if task.Source != domainagent.SourceFeishu || task.NotifyTarget == "" {
		return // 非飞书不主动推消息
	}
	// 先标记 reported:防 web 前端轮询在此期间查到 unreported 触发重复汇报
	if err := s.taskRepo.MarkReported(task.ID); err != nil {
		logger.ErrorWithFields("sub agent: mark reported before notify failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
		// 标记失败仍继续推送(尽力把消息送到)
	}
	if err := s.notifier.NotifyTaskCompleted(context.Background(), task.NotifyTarget, task); err != nil {
		logger.ErrorWithFields("sub agent: notify feishu failed",
			zap.Int64("task_id", task.ID), zap.String("open_id", task.NotifyTarget), zap.Error(err))
	}
}

// ErrTaskNotOwned 任务不属于该用户(属主校验失败)。
var ErrTaskNotOwned = errors.New("task not owned by user")

// SetNotifier 注入任务完成推送器(飞书 channel 启动后调,因 sender 在那时才创建)。
// 飞书未配置时保持 nil(web 任务靠轮询,不主动推)。
func (s *SubAgentService) SetNotifier(n TaskNotifier) {
	s.notifier = n
}

// SetCompletionPublisher 注入 web 实时推送器(08 §4.8;realtime.Hub)。
func (s *SubAgentService) SetCompletionPublisher(p TaskCompletionPublisher) {
	s.publisher = p
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
	instruction = BuildReportInstruction(unreported, false)
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
