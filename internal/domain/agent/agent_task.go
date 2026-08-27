package agent

import "time"

// AgentTask 后台 Agent 任务实体。主 Agent 通过 delegate 工具派活,
// 子 Agent 在后台 goroutine 独立执行,产出 Artifact 落库。
//
// 状态机:pending -> running -> completed | failed。
//
//	pending/running -> cancelled(用户/主 Agent 取消)。
//
// Reported 独立 bool:completed/failed 后等主 Agent 汇报才置 true,
// 避免轮询触发和兜底前置汇报重复汇报(见 08 技术方案 §4.5)。
//
// 任务按 UserID 隔离,用户只能查/汇报自己的任务(安全红线)。
type AgentTask struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	UserID       int64     `gorm:"index;not null"`         // 归属用户(任务按用户隔离)
	SubAgentType string    `gorm:"size:50;not null"`       // 溯源标签(可空,来自 taskSpec.Type;去角色后非角色,不 gate 机制)
	Goal         string    `gorm:"type:text;not null"`     // 委托目标(冗余存,task_spec.Goal 的快捷访问;兼容老代码)
	Status       string    `gorm:"size:20;not null;index"` // pending / running / completed / failed / cancelled
	Artifact     *string   `gorm:"type:text"`              // 子 Agent 最终产出,completed 时填
	ErrorMsg     *string   `gorm:"type:text"`              // failed 时填
	Reported     bool      `gorm:"not null;default:false"` // 是否已汇报给主 Agent(C 模式核心字段)
	Notes        []string  `gorm:"serializer:json"`        // 补充信息(running 态 update_task 追加,子 Agent 下轮注入上下文)
	TaskSpec     TaskSpec  `gorm:"serializer:json"`        // 任务包(goal+背景+交付物+完成标准+约束),替代裸 goal
	ParentTaskID *int64    `gorm:"index"`                  // 父任务 ID(预留:动态编排/任务链;当前 delegate 派出的为 nil)
	Source       string    `gorm:"size:20;index"`          // 来源渠道:"web"/"feishu"。空=web(老数据兼容)。决定完成时往哪推送
	NotifyTarget string    `gorm:"size:128"`               // 主动推送目标:feishu=open_id;web=空(靠轮询)。source=feishu 时必填
	CreatedAt    time.Time `gorm:"not null"`
	StartedAt    *time.Time
	CompletedAt  *time.Time
	CancelledAt  *time.Time `gorm:"index"` // cancelled 时填
}

// TableName 指定表名
func (AgentTask) TableName() string {
	return "agent_tasks"
}

// 任务状态常量
const (
	TaskStatusPending       = "pending"
	TaskStatusRunning       = "running"
	TaskStatusCompleted     = "completed"
	TaskStatusFailed        = "failed"
	TaskStatusCancelled     = "cancelled"      // 用户/主 Agent 取消(cancel_task)
	TaskStatusInputRequired = "input_required" // 子 Agent 主动要输入(update_task 补后回 running)
)

// IsTerminal 判断状态是否终态(不可再 query/update/cancel)。
// 注意:input_required 不是终态(补输入后回 running)。
func (t *AgentTask) IsTerminal() bool {
	switch t.Status {
	case TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled:
		return true
	}
	return false
}

// IsActive 判断任务是否还在运行中(running 或 input_required,可补/可取消)。
func (t *AgentTask) IsActive() bool {
	switch t.Status {
	case TaskStatusPending, TaskStatusRunning, TaskStatusInputRequired:
		return true
	}
	return false
}

// 任务来源渠道常量(决定完成时往哪推送汇报)。
const (
	SourceWeb    = "web"    // Web 端:完成靠前端轮询 GET /agent/tasks + 前置汇报
	SourceFeishu = "feishu" // 飞书:完成时主动推送到 open_id(NotifyTarget)
)

// NewAgentTask 创建一个 pending 状态的新任务。
// taskSpec 任务包(含 goal+背景+交付物+完成标准),其 Type 字段作 AgentTask.SubAgentType 溯源标签(可空)。
// goal 单独冗余存一份;source 来源渠道(web/feishu);notifyTarget 主动推送目标(feishu=open_id;web=空)。
func NewAgentTask(userID int64, taskSpec TaskSpec, source, notifyTarget string) *AgentTask {
	return &AgentTask{
		UserID:       userID,
		SubAgentType: taskSpec.Type, // 溯源标签(非角色,不 gate 机制)
		Goal:         taskSpec.Goal, // 冗余快捷访问
		TaskSpec:     taskSpec,
		Status:       TaskStatusPending,
		Source:       source,
		NotifyTarget: notifyTarget,
		CreatedAt:    time.Now(),
	}
}
