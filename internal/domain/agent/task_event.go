package agent

import "time"

// TaskEvent 任务状态变化事件(10-规划 §2.2)。
//
// 记录任务生命周期内的状态变化,供:
//   - 审计/可观测(任务经过哪些状态、何时)
//   - 未来事件驱动(替代 ListCompletedUnreported 轮询;Postgres LISTEN/NOTIFY 推送)
//
// 存 agent_task_events 表。Phase 7a(16-路线图 §14)起事件与状态迁移同事务写入
// (repo TransitionStatusWithEvent/CreateWithEvent),序号由 agent_tasks.version 派生,
// (task_id, sequence) 唯一约束兜底幂等——不再依赖进程内 eventSeq map。
// 消费方当前仍轮询 task 表;events 表为未来推送铺路。
type TaskEvent struct {
	ID          int64 `json:"id" gorm:"primaryKey;autoIncrement"`
	TaskID      int64 `json:"task_id" gorm:"not null;uniqueIndex:idx_task_event_task_seq,priority:1"`
	EventType   string `json:"event_type" gorm:"size:50;index"` // task.submitted/accepted/running/input_required/completed/failed/cancelled
	Sequence    int    `json:"sequence" gorm:"not null;uniqueIndex:idx_task_event_task_seq,priority:2"` // 任务内事件序号(= 迁移后的 task.version,幂等用)
	Payload     map[string]any `json:"payload,omitempty" gorm:"serializer:json"`
	SourceAgent string         `json:"source_agent,omitempty" gorm:"size:50"` // 事件来源(main/sub agent)
	OccurredAt  time.Time      `json:"occurred_at" gorm:"not null"`
}

// TableName 指定表名
func (TaskEvent) TableName() string {
	return "agent_task_events"
}

// 事件类型常量(对应状态机转换)。
const (
	EventTaskSubmitted     = "task.submitted"      // 任务创建(pending)
	EventTaskRunning       = "task.running"        // 开始执行
	EventTaskInputRequired = "task.input_required" // 子 Agent 要输入
	EventTaskCompleted     = "task.completed"      // 完成
	EventTaskFailed        = "task.failed"         // 失败
	EventTaskCancelled     = "task.cancelled"      // 取消
	EventArtifactCreated   = "artifact.created"    // 产物产生(预留,#18 artifact 落库时)
)

// NewTaskEvent 构造一个事件。
func NewTaskEvent(taskID int64, eventType string, sequence int, source string) TaskEvent {
	return TaskEvent{
		TaskID:      taskID,
		EventType:   eventType,
		Sequence:    sequence,
		SourceAgent: source,
		OccurredAt:  time.Now(),
	}
}
