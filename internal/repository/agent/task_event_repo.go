package agent

import (
	"gorm.io/gorm"

	domainagent "omnibot/internal/domain/agent"
)

// TaskEventRepository 任务事件仓储(10-规划 §2.2)。
type TaskEventRepository interface {
	// Create 写入一个事件。
	Create(e *domainagent.TaskEvent) error
	// ListByTaskID 读取某任务的事件历史(按 sequence 升序)。
	ListByTaskID(taskID int64) ([]*domainagent.TaskEvent, error)
}

type taskEventRepository struct {
	db *gorm.DB
}

// NewTaskEventRepository 创建事件仓储
func NewTaskEventRepository(db *gorm.DB) TaskEventRepository {
	return &taskEventRepository{db: db}
}

func (r *taskEventRepository) Create(e *domainagent.TaskEvent) error {
	return r.db.Create(e).Error
}

func (r *taskEventRepository) ListByTaskID(taskID int64) ([]*domainagent.TaskEvent, error) {
	var events []*domainagent.TaskEvent
	err := r.db.Where("task_id = ?", taskID).Order("sequence ASC").Find(&events).Error
	if err != nil {
		return nil, err
	}
	return events, nil
}
