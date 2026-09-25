package agent

import (
	"gorm.io/gorm"

	"omnibot/internal/domain/agent"
)

// AgentTaskRepository 后台 Agent 任务仓储接口(08 技术方案 §4.2)。
type AgentTaskRepository interface {
	Create(task *agent.AgentTask) error
	GetByID(id int64) (*agent.AgentTask, error)
	// TransitionStatus CAS 状态迁移(Phase 5,16-路线图 §11/§12):
	// UPDATE ... WHERE id=? AND status=from,仅当当前状态等于 from 才生效。
	// 迁移先过 domainagent.CanTransition 状态机表,非法迁移直接拒绝(false, nil)。
	// 返回 false = 迁移未发生(并发竞争或非法迁移),调用方据此决策,不作为错误。
	// completed 填 artifact,failed 填 errorMsg;时间戳随目标状态自动记。
	TransitionStatus(id int64, from, to string, artifact *string, errorMsg *string) (bool, error)
	MarkReported(id int64) error
	// ListCompletedUnreported 返回该用户已 completed/failed 但未汇报的任务(C 模式核心查询)。
	// 包含 failed 任务--失败也要汇报(08 §9)。
	ListCompletedUnreported(userID int64) ([]*agent.AgentTask, error)
	// ListByUser 列出该用户的任务(按创建时间倒序,limit 限上限)。
	ListByUser(userID int64, limit int) ([]*agent.AgentTask, error)
	// UpdateGoal 改 goal(pending 态 update_task 用)。
	UpdateGoal(id int64, goal string) error
	// AppendNote 追加补充信息到 Notes(running 态 update_task 用)。
	AppendNote(id int64, note string) error
}

// GormAgentTaskRepository GORM 实现
type GormAgentTaskRepository struct {
	db *gorm.DB
}

// NewAgentTaskRepository 创建仓储
func NewAgentTaskRepository(db *gorm.DB) AgentTaskRepository {
	return &GormAgentTaskRepository{db: db}
}

func (r *GormAgentTaskRepository) Create(task *agent.AgentTask) error {
	return r.db.Create(task).Error
}

func (r *GormAgentTaskRepository) GetByID(id int64) (*agent.AgentTask, error) {
	var t agent.AgentTask
	err := r.db.Where("id = ?", id).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// TransitionStatus CAS 状态迁移(Phase 5)。UPDATE ... WHERE id=? AND status=from,
// RowsAffected==0 → 返回 false。先过 CanTransition 状态机表(非法迁移 false, nil)。
// 时间戳随目标状态:running→started_at,completed/failed→completed_at,cancelled→cancelled_at。
func (r *GormAgentTaskRepository) TransitionStatus(id int64, from, to string, artifact *string, errorMsg *string) (bool, error) {
	if !agent.CanTransition(from, to) {
		return false, nil
	}
	updates := map[string]interface{}{
		"status": to,
	}
	if artifact != nil {
		updates["artifact"] = *artifact
	}
	if errorMsg != nil {
		updates["error_msg"] = *errorMsg
	}
	switch to {
	case agent.TaskStatusCompleted, agent.TaskStatusFailed:
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	case agent.TaskStatusRunning:
		updates["started_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	case agent.TaskStatusCancelled:
		updates["cancelled_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	res := r.db.Model(&agent.AgentTask{}).
		Where("id = ? AND status = ?", id, from).
		Updates(updates)
	return res.RowsAffected == 1, res.Error
}

func (r *GormAgentTaskRepository) MarkReported(id int64) error {
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Update("reported", true).Error
}

func (r *GormAgentTaskRepository) ListCompletedUnreported(userID int64) ([]*agent.AgentTask, error) {
	var tasks []*agent.AgentTask
	err := r.db.Where("user_id = ? AND reported = ? AND status IN ?",
		userID, false, []string{agent.TaskStatusCompleted, agent.TaskStatusFailed}).
		Order("completed_at ASC").
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *GormAgentTaskRepository) ListByUser(userID int64, limit int) ([]*agent.AgentTask, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var tasks []*agent.AgentTask
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// UpdateGoal 改 goal(pending 态 update_task 用)。
func (r *GormAgentTaskRepository) UpdateGoal(id int64, goal string) error {
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Update("goal", goal).Error
}

// AppendNote 追加补充信息到 Notes。Notes 是 JSON 序列化字段(serializer:json),
// 单列 Update/Updates(map) 不走 serializer,需读出现有值手动 append,再用结构体 Updates 写回
// (结构体字段才触发 serializer:json 正确 marshal 为 JSON 字符串)。
func (r *GormAgentTaskRepository) AppendNote(id int64, note string) error {
	var t agent.AgentTask
	if err := r.db.Where("id = ?", id).First(&t).Error; err != nil {
		return err
	}
	t.Notes = append(t.Notes, note)
	// 只更新 notes 列:Select 限定,走 serializer 正确 marshal。
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Select("notes").Updates(t).Error
}
