package chat

import (
	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
)

// AgentStepRepository Agent 运行步骤链仓储（v1.5.5）。
// 存一轮对话的完整执行链（LLM 调用 + 工具调用），供离线记录与分析；不对外暴露。
type AgentStepRepository interface {
	// CreateBatch 批量写入步骤。空切片直接返回 nil。
	CreateBatch(steps []*conversation.AgentStep) error
	// ListByMessageID 读取某条消息（一轮对话）的步骤链，按 seq 正序还原时序。
	ListByMessageID(messageID int64) ([]*conversation.AgentStep, error)
	// ListByTaskID 读取某个后台子 Agent 任务的步骤链,按 seq 正序还原时序。
	ListByTaskID(taskID int64) ([]*conversation.AgentStep, error)
	// ListByUserID 按用户读取最近的步骤（id 倒序）。
	ListByUserID(userID int64, limit int) ([]*conversation.AgentStep, error)
}

type agentStepRepository struct {
	db *gorm.DB
}

// NewAgentStepRepository 创建 Agent 步骤链仓储
func NewAgentStepRepository(db *gorm.DB) AgentStepRepository {
	return &agentStepRepository{db: db}
}

func (r *agentStepRepository) CreateBatch(steps []*conversation.AgentStep) error {
	if len(steps) == 0 {
		return nil
	}
	return r.db.Create(steps).Error
}

func (r *agentStepRepository) ListByMessageID(messageID int64) ([]*conversation.AgentStep, error) {
	var steps []*conversation.AgentStep
	err := r.db.Where("message_id = ?", messageID).
		Order("seq ASC").
		Find(&steps).Error
	return steps, err
}

func (r *agentStepRepository) ListByTaskID(taskID int64) ([]*conversation.AgentStep, error) {
	var steps []*conversation.AgentStep
	err := r.db.Where("task_id = ?", taskID).
		Order("seq ASC").
		Find(&steps).Error
	return steps, err
}

func (r *agentStepRepository) ListByUserID(userID int64, limit int) ([]*conversation.AgentStep, error) {
	var steps []*conversation.AgentStep
	err := r.db.Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).
		Find(&steps).Error
	return steps, err
}
