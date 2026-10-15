package agent

import "time"

// PersistentTask 持久任务(25 号):周期调度的宿主。
// 用户说"以后每天早上抓一次 AI 资讯"→ 主 Agent 经 manage_persistent_task 创建;
// 调度器按 Cron 到点把 TaskSpecJSON 反序列化后走 StartTask 派一次性实例任务
// (agent_tasks,source=schedule),执行/汇报链路与 delegate 派活完全同构。
//
// 设计要点:
//   - 本表只管"何时派活",不参与执行——实例任务失败不影响宿主(除非连续失败);
//   - NextRunAt 物化列是调度器的扫描锚(带 index),触发时 CAS 防重入;
//   - 不追帧:停机期间错过的触发丢弃,启动时统一重算 NextRunAt。
type PersistentTask struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	UserID       int64     `gorm:"index;not null"`
	Name         string    `gorm:"size:64"`            // 短名(人读展示)
	Goal         string    `gorm:"type:text;not null"` // 委托目标(与 TaskSpec.Goal 一致,快捷访问)
	TaskSpecJSON string    `gorm:"type:text;not null"` // TaskSpec 快照(合同跨执行复用)
	Cron         string    `gorm:"size:64;not null"`   // 5 字段 cron,本地时区
	Enabled      bool      `gorm:"not null;default:true"`
	NextRunAt    time.Time `gorm:"index;not null"` // 下次触发点
	LastRunAt    *time.Time
	LastTaskID   *int64 // 最近一次实例任务(跳转/排查用)
	FailStreak   int    `gorm:"not null;default:0"` // 连续失败次数,成功清零;≥3 自动停用
	// Source/NotifyTarget 创建渠道快照:实例任务派活时继承——source 决定汇报去向
	// (feishu=主动推送/web=轮询),不能标 "schedule"(否则 notifyCompleted 不推送)。
	Source       string `gorm:"size:20"`
	NotifyTarget string `gorm:"size:128"`
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

// TableName 指定表名
func (PersistentTask) TableName() string {
	return "persistent_tasks"
}

// MaxPersistentTasksPerUser 每用户持久任务上限(防滥用)。
const MaxPersistentTasksPerUser = 20

// AutoPauseFailStreak 连续失败达到该次数即自动停用(频率克制:最后一次失败的
// 正常汇报已到用户,停用本身不再发通知,状态在列表可见)。
const AutoPauseFailStreak = 3
