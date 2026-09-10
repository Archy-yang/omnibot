package memory

import "time"

// 事项状态(M6,助理人视角 §M6.2)
const (
	MatterStatusActive   = "active"   // 推进中
	MatterStatusDone     = "done"     // 已完结
	MatterStatusArchived = "archived" // 已搁置/不再跟进
)

// Matter 事项(M6 记忆重构,助理人视角):Agent 像真人助理那样,
// 对"正在推进的事"维护一条当前状态认知——新进展来时覆写 StateDesc,
// 而不是像对话切片那样追加流水账。检索"这件事到哪了"直接命中。
type Matter struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"not null;uniqueIndex:idx_user_matter_title,priority:1"`
	Title          string    `gorm:"size:128;not null;uniqueIndex:idx_user_matter_title,priority:2"` // 同用户内唯一:防同事项裂成多行
	StateDesc      string    `gorm:"type:text"`                                                      // 当前状态描述(覆写式更新,只留仍有效信息+最新进展)
	Status         string    `gorm:"size:20;not null;default:active"`                                // active/done/archived
	LastMsgID      int64     // 最近一次相关消息(水位簿记)
	Embedding      []float32 `gorm:"serializer:json"` // (Title+StateDesc) 向量化,供事项命中检索
	EmbeddingModel string    `gorm:"size:100"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time `gorm:"not null"`
}

func (Matter) TableName() string {
	return "matters"
}

// NormalizeStatus 归一事项状态:非法/空 → active。
func NormalizeStatus(s string) string {
	switch s {
	case MatterStatusActive, MatterStatusDone, MatterStatusArchived:
		return s
	default:
		return MatterStatusActive
	}
}
