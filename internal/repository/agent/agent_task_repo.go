package agent

import (
	"gorm.io/gorm"

	"omnibot/internal/domain/agent"
)

// AgentTaskRepository 后台 Agent 任务仓储接口(08 技术方案 §4.2)。
type AgentTaskRepository interface {
	Create(task *agent.AgentTask) error
	GetByID(id int64) (*agent.AgentTask, error)
	// UpdateStatus 更新状态;completed 时填 artifact,failed 时填 errorMsg。
	UpdateStatus(id int64, status string, artifact *string, errorMsg *string) error
	MarkReported(id int64) error
	// ListCompletedUnreported 返回该用户已 completed/failed 但未汇报的任务(C 模式核心查询)。
	// 包含 failed 任务--失败也要汇报(08 §9)。
	ListCompletedUnreported(userID int64) ([]*agent.AgentTask, error)
	// ListByUser 列出该用户的任务(按创建时间倒序,limit 限上限)。
	ListByUser(userID int64, limit int) ([]*agent.AgentTask, error)
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

func (r *GormAgentTaskRepository) UpdateStatus(id int64, status string, artifact *string, errorMsg *string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if artifact != nil {
		updates["artifact"] = *artifact
	}
	if errorMsg != nil {
		updates["error_msg"] = *errorMsg
	}
	// completed/failed 时记录完成时间
	if status == agent.TaskStatusCompleted || status == agent.TaskStatusFailed {
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	if status == agent.TaskStatusRunning {
		updates["started_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).Updates(updates).Error
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
