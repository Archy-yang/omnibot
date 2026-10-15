package agent

// persistent_task_service.go — 持久任务服务与调度器(25 号)。
// 宿主(persistent_tasks)只管"何时派活":到点把 TaskSpec 快照反序列化后走
// StartTask 派一次性实例任务(source=schedule),执行/汇报链路与 delegate 同构。
// 语义要点(方案 §1.1):不追帧;连续失败 ≥3 自动停用且不额外发通知。

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"

	cronv3 "github.com/robfig/cron/v3"
)

// ErrPersistentTaskNotFound 持久任务不存在或不属于该用户(归属隔离统一 404,防存在性泄露)。
var ErrPersistentTaskNotFound = errors.New("persistent task not found")

// cronParser 5 字段 cron(本地时区):分 时 日 月 周。不支持秒/描述符(@daily)——
// LLM 换算的输出面越窄越不易错。
var cronParser = cronv3.NewParser(cronv3.Minute | cronv3.Hour | cronv3.Dom | cronv3.Month | cronv3.Dow)

// ParseCronExpr 校验并规范化 cron 表达式(原样返回,仅做可解析性验证)。
func ParseCronExpr(expr string) (string, error) {
	if _, err := cronParser.Parse(expr); err != nil {
		return "", fmt.Errorf("cron 表达式非法(%s): %w", expr, err)
	}
	return expr, nil
}

// CreatePersistentTaskInput 创建入参。SpecJSON 为 TaskSpec 合同快照(JSON),
// 空时由 Goal 构造最小合同;工具层负责把 deliverables 等装配进 spec。
type CreatePersistentTaskInput struct {
	Name         string
	Goal         string
	Cron         string
	Source       string // 创建渠道快照(web/feishu),实例汇报去向
	NotifyTarget string
	SpecJSON     string
}

// PersistentTaskService 持久任务服务。
type PersistentTaskService struct {
	repo repoagent.PersistentTaskRepository
}

// NewPersistentTaskService 创建服务。
func NewPersistentTaskService(repo repoagent.PersistentTaskRepository) *PersistentTaskService {
	return &PersistentTaskService{repo: repo}
}

// Create 校验 cron 与上限后落库,next_run_at = cron.Next(now)。
// 返回 (id, 首次执行时间, err)——确认文案带上 next,让用户当场核对周期是否合意。
func (s *PersistentTaskService) Create(userID int64, in CreatePersistentTaskInput) (int64, time.Time, error) {
	if in.Goal == "" {
		return 0, time.Time{}, fmt.Errorf("goal 不能为空")
	}
	schedule, err := cronParser.Parse(in.Cron)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("cron 表达式非法(%s)", in.Cron)
	}

	n, err := s.repo.CountByUser(userID)
	if err != nil {
		return 0, time.Time{}, err
	}
	if n >= domainagent.MaxPersistentTasksPerUser {
		return 0, time.Time{}, fmt.Errorf("持久任务已达上限(%d),请先清理不再需要的任务", domainagent.MaxPersistentTasksPerUser)
	}

	specJSON := in.SpecJSON
	if specJSON == "" {
		spec := domainagent.NewTaskSpec(in.Goal)
		raw, err := json.Marshal(spec)
		if err != nil {
			return 0, time.Time{}, err
		}
		specJSON = string(raw)
	}

	now := time.Now()
	pt := &domainagent.PersistentTask{
		UserID:       userID,
		Name:         in.Name,
		Goal:         in.Goal,
		TaskSpecJSON: specJSON,
		Cron:         in.Cron,
		Enabled:      true,
		NextRunAt:    schedule.Next(now),
		Source:       in.Source,
		NotifyTarget: in.NotifyTarget,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.repo.Create(pt); err != nil {
		return 0, time.Time{}, err
	}
	return pt.ID, pt.NextRunAt, nil
}

// List 该用户的持久任务(创建时间倒序)。
func (s *PersistentTaskService) List(userID int64) ([]*domainagent.PersistentTask, error) {
	return s.repo.ListByUser(userID)
}

// Pause 停用(保留配置,可恢复)。
func (s *PersistentTaskService) Pause(userID, id int64) error {
	pt, err := s.owned(userID, id)
	if err != nil {
		return err
	}
	return s.repo.UpdateFields(pt.ID, map[string]interface{}{"enabled": false, "updated_at": time.Now()})
}

// Resume 恢复并重算 next_run_at(停机/暂停期间错过的触发不追帧)。
func (s *PersistentTaskService) Resume(userID, id int64) error {
	pt, err := s.owned(userID, id)
	if err != nil {
		return err
	}
	schedule, err := cronParser.Parse(pt.Cron)
	if err != nil {
		return fmt.Errorf("存量 cron 非法(%s): %w", pt.Cron, err)
	}
	return s.repo.UpdateFields(pt.ID, map[string]interface{}{
		"enabled": true, "fail_streak": 0, "next_run_at": schedule.Next(time.Now()),
		"updated_at": time.Now(),
	})
}

// Delete 物理删除。
func (s *PersistentTaskService) Delete(userID, id int64) error {
	pt, err := s.owned(userID, id)
	if err != nil {
		return err
	}
	return s.repo.Delete(pt.ID)
}

// owned 归属校验:未命中或他人任务统一 ErrPersistentTaskNotFound(防存在性泄露)。
func (s *PersistentTaskService) owned(userID, id int64) (*domainagent.PersistentTask, error) {
	pt, err := s.repo.GetByID(id)
	if err != nil || pt == nil || pt.UserID != userID {
		return nil, ErrPersistentTaskNotFound
	}
	return pt, nil
}

// HandleFinished 任务终态回调(SubAgentService finishedObserver):维护 fail_streak。
// 只关心 source=schedule 的实例;cancelled 是用户意志,不计失败。
func (s *PersistentTaskService) HandleFinished(task *domainagent.AgentTask) {
	if task == nil || task.ScheduleID == nil {
		return
	}
	pt, err := s.repo.GetByID(*task.ScheduleID)
	if err != nil || pt == nil {
		return
	}
	switch task.Status {
	case domainagent.TaskStatusCompleted:
		if pt.FailStreak != 0 {
			_ = s.repo.UpdateFields(pt.ID, map[string]interface{}{"fail_streak": 0, "updated_at": time.Now()})
		}
	case domainagent.TaskStatusFailed:
		streak := pt.FailStreak + 1
		fields := map[string]interface{}{"fail_streak": streak, "updated_at": time.Now()}
		if streak >= domainagent.AutoPauseFailStreak {
			fields["enabled"] = false // 自动停用不发通知:最后一次失败的正常汇报已到用户
		}
		_ = s.repo.UpdateFields(pt.ID, fields)
	}
}
