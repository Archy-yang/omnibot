package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
	"omnibot/pkg/logger"
)

// SubAgentRunner 子 Agent 执行器接口:按 card 的 prompt+工具集跑一次 Agent,返回最终产出。
// 生产实现见 subAgentRunnerImpl(用 ReActAgent + 系统默认 LLM);测试可 mock。
type SubAgentRunner interface {
	// Run 按 card 配置执行子 Agent。goal 已填入 card.PromptTemplate 生成 system prompt。
	// 返回子 Agent 的最终回复(FinalResponse)作为 Artifact。
	Run(ctx context.Context, card domainagent.SubAgentCard, goal string) (string, error)
}

// SubAgentService 后台子 Agent 任务服务(08 技术方案 §4.3)。
//
// StartTask 建任务 + 起 goroutine 后台执行(executeTask),立即返回 task_id(异步);
// executeTask 调 SubAgentRunner 跑子 Agent,写 Artifact,更新状态;
// GetCompletedUnreported 供主 Agent 前置汇报查询;MarkReported 汇报后标记。
type SubAgentService struct {
	taskRepo repoagent.AgentTaskRepository
	registry *SubAgentRegistry
	runner   SubAgentRunner
}

// NewSubAgentService 创建子 Agent 服务。
func NewSubAgentService(
	taskRepo repoagent.AgentTaskRepository,
	registry *SubAgentRegistry,
	runner SubAgentRunner,
) *SubAgentService {
	return &SubAgentService{
		taskRepo: taskRepo,
		registry: registry,
		runner:   runner,
	}
}

// StartTask 建任务 + 起 goroutine 后台执行,立即返回 task_id(异步,不阻塞调用方)。
// subAgentType 未注册返回 error(早失败,不静默)。
func (s *SubAgentService) StartTask(ctx context.Context, userID int64, subAgentType, goal string) (int64, error) {
	if _, ok := s.registry.Get(subAgentType); !ok {
		return 0, fmt.Errorf("sub agent %q not registered", subAgentType)
	}

	task := domainagent.NewAgentTask(userID, subAgentType, goal)
	if err := s.taskRepo.Create(task); err != nil {
		return 0, fmt.Errorf("create agent task: %w", err)
	}

	// 后台执行。用独立 context(不继承请求 ctx,请求结束子 Agent 继续跑)。
	go s.executeTask(context.Background(), task)
	return task.ID, nil
}

// executeTask 跑子 Agent,写 Artifact,更新状态。goroutine 内执行,panic 兜底。
func (s *SubAgentService) executeTask(_ context.Context, task *domainagent.AgentTask) {
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

	// running
	if err := s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusRunning, nil, nil); err != nil {
		logger.ErrorWithFields("sub agent: update to running failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
		return
	}

	// 带 card.Timeout 执行
	ctx, cancel := context.WithTimeout(context.Background(), card.Timeout)
	defer cancel()

	artifact, err := s.runner.Run(ctx, card, task.Goal)
	if err != nil {
		errMsg := sanitizeSubAgentError(err)
		logger.ErrorWithFields("sub agent: run failed",
			zap.Int64("task_id", task.ID),
			zap.String("sub_agent", task.SubAgentType),
			zap.Error(err),
		)
		_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		return
	}

	if strings.TrimSpace(artifact) == "" {
		errMsg := "子 Agent 未产出有效结果"
		_ = s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusFailed, nil, &errMsg)
		return
	}

	if err := s.taskRepo.UpdateStatus(task.ID, domainagent.TaskStatusCompleted, &artifact, nil); err != nil {
		logger.ErrorWithFields("sub agent: update to completed failed",
			zap.Int64("task_id", task.ID), zap.Error(err))
	}
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

// GetPendingReportContext 返回待汇报的回执指令 + 对应任务 ID(供主对话前置汇报兜底)。
// 封装:查未汇报任务 + 构造回执指令。无待汇报任务返回空 instruction + nil taskIDs。
// web/飞书主对话 handler 在调主 Agent Run 前调用此方法,有指令则 prepend 到上下文。
func (s *SubAgentService) GetPendingReportContext(userID int64) (instruction string, taskIDs []int64) {
	unreported, err := s.taskRepo.ListCompletedUnreported(userID)
	if err != nil || len(unreported) == 0 {
		return "", nil
	}
	instruction = BuildReportInstruction(s.registry, unreported)
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
