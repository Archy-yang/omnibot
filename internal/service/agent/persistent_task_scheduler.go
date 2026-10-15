package agent

// persistent_task_scheduler.go — 持久任务调度器(25 号)。
// 30s tick 扫 ListDue → CAS 抢占(防重入)→ StartTask 派实例任务。
// 启动恢复:enabled 任务统一重算 next_run_at,停机期间错过的触发不追帧。

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// TaskStarter 派活窄接口(SubAgentService 满足,单测 stub)。
type TaskStarter interface {
	StartTask(ctx context.Context, userID int64, taskSpec domainagent.TaskSpec, source, notifyTarget string, originTurnID int64) (int64, error)
}

// PersistentTaskScheduler 调度器。
type PersistentTaskScheduler struct {
	repo    repoagent.PersistentTaskRepository
	service *PersistentTaskService // HandleFinished 终态回调的宿主
	starter TaskStarter
}

// NewPersistentTaskScheduler 创建调度器(starter 缺省为 nil,Start 前必须 WireStarter)。
func NewPersistentTaskScheduler(repo repoagent.PersistentTaskRepository, service *PersistentTaskService) *PersistentTaskScheduler {
	return &PersistentTaskScheduler{repo: repo, service: service}
}

// WireStarter 注入派活执行器(装配层在 SubAgentService 就绪后调用)。
func (s *PersistentTaskScheduler) WireStarter(starter TaskStarter) {
	s.starter = starter
}

// ObserveFinished 实现终态观察者签名,装配层把它挂到 SubAgentService。
func (s *PersistentTaskScheduler) ObserveFinished(task *domainagent.AgentTask) {
	s.service.HandleFinished(task)
}

// Recover 启动恢复:enabled 任务重算 next_run_at(不追帧)。服务启动时调用一次。
func (s *PersistentTaskScheduler) Recover(now time.Time) error {
	list, err := s.repo.ListEnabled()
	if err != nil {
		return err
	}
	for _, pt := range list {
		schedule, err := cronParser.Parse(pt.Cron)
		if err != nil {
			// 存量 cron 非法(理论不可达:创建已校验)——停用并记日志,不让坏数据卡死恢复
			logger.ErrorWithFields("persistent task: invalid cron on recover, disable",
				zap.Int64("id", pt.ID), zap.String("cron", pt.Cron), zap.Error(err))
			_ = s.repo.UpdateFields(pt.ID, map[string]interface{}{"enabled": false})
			continue
		}
		if err := s.repo.UpdateFields(pt.ID, map[string]interface{}{"next_run_at": schedule.Next(now)}); err != nil {
			return err
		}
	}
	return nil
}

// Start 启动调度循环(阻塞,应在 goroutine 中运行;ctx 结束即退出)。
func (s *PersistentTaskScheduler) Start(ctx context.Context) {
	if err := s.Recover(time.Now()); err != nil {
		logger.ErrorWithFields("persistent task: recover failed", zap.Error(err))
	}
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *PersistentTaskScheduler) tick(ctx context.Context) {
	due, err := s.repo.ListDue(time.Now(), 50)
	if err != nil {
		logger.ErrorWithFields("persistent task: list due failed", zap.Error(err))
		return
	}
	for _, pt := range due {
		if _, err := s.TriggerOne(pt); err != nil {
			logger.ErrorWithFields("persistent task: trigger failed",
				zap.Int64("id", pt.ID), zap.Error(err))
		}
	}
	_ = ctx
}

// TriggerOne 触发单个到期任务:CAS 抢占 → StartTask → 回填 last_task_id。
// 返回是否真正触发(false=CAS 被抢/派活失败前的状态漂移)。
func (s *PersistentTaskScheduler) TriggerOne(pt *domainagent.PersistentTask) (bool, error) {
	schedule, err := cronParser.Parse(pt.Cron)
	if err != nil {
		// 创建时已校验,理论不可达;防御性停用
		logger.ErrorWithFields("persistent task: invalid cron at trigger, disable",
			zap.Int64("id", pt.ID), zap.String("cron", pt.Cron), zap.Error(err))
		_ = s.repo.UpdateFields(pt.ID, map[string]interface{}{"enabled": false})
		return false, nil
	}

	now := time.Now()
	newNext := schedule.Next(now)
	ok, err := s.repo.ClaimDue(pt.ID, pt.NextRunAt, newNext, now)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // 已被抢占(并发/状态漂移),跳过
	}

	var spec domainagent.TaskSpec
	if err := json.Unmarshal([]byte(pt.TaskSpecJSON), &spec); err != nil {
		// 合同快照损坏:如实按失败计(累计到 auto-pause),不静默吞
		logger.ErrorWithFields("persistent task: unmarshal spec failed",
			zap.Int64("id", pt.ID), zap.Error(err))
		s.service.HandleFinished(&domainagent.AgentTask{
			Status: domainagent.TaskStatusFailed, ScheduleID: &pt.ID,
		})
		return false, fmt.Errorf("unmarshal task spec: %w", err)
	}

	// 实例继承创建渠道的 source/notify_target:source 决定汇报去向(notifyCompleted
	// 仅对 feishu 主动推送),标 "schedule" 会让汇报静默丢失——这是方案评审后修正的关键点
	taskID, err := s.starter.StartTask(context.Background(), pt.UserID, spec, pt.Source, pt.NotifyTarget, 0)
	if err != nil {
		logger.ErrorWithFields("persistent task: start instance failed",
			zap.Int64("id", pt.ID), zap.Error(err))
		s.service.HandleFinished(&domainagent.AgentTask{
			Status: domainagent.TaskStatusFailed, ScheduleID: &pt.ID,
		})
		return false, err
	}
	if err := s.repo.SetLastTask(pt.ID, taskID); err != nil {
		logger.ErrorWithFields("persistent task: set last task failed",
			zap.Int64("id", pt.ID), zap.Error(err))
	}
	logger.InfoWithFields("persistent task: triggered",
		zap.Int64("id", pt.ID), zap.Int64("task_id", taskID), zap.Time("next", newNext))
	return true, nil
}
