package agent

import "time"

// AgentTask 后台 Agent 任务实体。主 Agent 通过 delegate 工具派活,
// 子 Agent 在后台 goroutine 独立执行,产出 Artifact 落库。
//
// 状态机:pending -> running -> completed | failed。
// Reported 独立 bool:completed/failed 后等主 Agent 汇报才置 true,
// 避免轮询触发和兜底前置汇报重复汇报(见 08 技术方案 §4.5)。
//
// 任务按 UserID 隔离,用户只能查/汇报自己的任务(安全红线)。
type AgentTask struct {
	ID           int64      `gorm:"primaryKey;autoIncrement"`
	UserID       int64      `gorm:"index;not null"`         // 归属用户(任务按用户隔离)
	SubAgentType string     `gorm:"size:50;not null"`       // 子 Agent 类型标识,如 "researcher"
	Goal         string     `gorm:"type:text;not null"`     // 主 Agent 生成的委托目标(已填入子 Agent prompt)
	Status       string     `gorm:"size:20;not null;index"` // pending / running / completed / failed
	Artifact     *string    `gorm:"type:text"`              // 子 Agent 最终产出,completed 时填
	ErrorMsg     *string    `gorm:"type:text"`              // failed 时填
	Reported     bool       `gorm:"not null;default:false"` // 是否已汇报给主 Agent(C 模式核心字段)
	CreatedAt    time.Time  `gorm:"not null"`
	StartedAt    *time.Time
	CompletedAt  *time.Time
}

// TableName 指定表名
func (AgentTask) TableName() string {
	return "agent_tasks"
}

// 任务状态常量
const (
	TaskStatusPending   = "pending"
	TaskStatusRunning   = "running"
	TaskStatusCompleted = "completed"
	TaskStatusFailed    = "failed"
)

// NewAgentTask 创建一个 pending 状态的新任务。
func NewAgentTask(userID int64, subAgentType, goal string) *AgentTask {
	return &AgentTask{
		UserID:       userID,
		SubAgentType: subAgentType,
		Goal:         goal,
		Status:       TaskStatusPending,
		CreatedAt:    time.Now(),
	}
}
