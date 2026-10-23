package butler

import "time"

// 26 号主动管家:管家的每次主动开口(或被克制闸门拦下)都留痕——
// 主动说过什么必须可回看、可反馈;dedup_key 是 7 天冷却与 v1.1 主题静音的锚。

// NoteStatus 记录状态
const (
	NoteStatusDelivered = "delivered" // 已送达(或已落库待送达,N2 接线前等价于"开口了")
	NoteStatusSkipped   = "skipped"   // 裁决通过但被克制闸门拦下(冷却中/超日限)
)

// NoteKind 信号类型
const (
	NoteKindMatterDue      = "matter_due"       // 事项将到期/临近信号
	NoteKindLoopStale      = "loop_stale"       // 未决 loop 搁置太久
	NoteKindRecentFollowup = "recent_followup"  // 近况值得跟进
	// NoteKindCloseSuggest 收口建议(28号 N1):事项/待办时间线已有结论,建议用户确认后收口。
	// dedup_key 加 :close 后缀,与普通提醒同一 7 天冷却池。
	NoteKindCloseSuggest = "close_suggest"
	// NoteKindGoalNudge 目标推进建议(29号 N2):目标临近/过期未推进,给具体可执行的推进建议
	// (派活需用户对话确认)。dedup_key 加 :goal 后缀,同一 7 天冷却池。
	NoteKindGoalNudge = "goal_nudge"
)

// ProactiveNote 主动开口记录。
type ProactiveNote struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	UserID      int64  `gorm:"index;not null"`
	Kind        string `gorm:"size:32;not null"`   // matter_due / loop_stale / recent_followup
	Content     string `gorm:"type:text;not null"` // 管家口吻的主动消息
	MatterID    *int64 `gorm:"index"`              // 溯源事项(可空)
	DedupKey    string `gorm:"size:128;not null"`  // 如 "matter:42"/"loop:101"——冷却与 v1.1 静音的锚
	Status      string `gorm:"size:20;not null"`   // delivered / skipped
	Feedback    string `gorm:"size:20"`            // positive / negative(空=未反馈)
	CreatedAt   time.Time `gorm:"not null"`
	DeliveredAt *time.Time
}

func (ProactiveNote) TableName() string {
	return "proactive_notes"
}
