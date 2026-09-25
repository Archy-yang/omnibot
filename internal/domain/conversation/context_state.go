package conversation

import (
	"time"
)

// ConversationContextState Conversation 的 Compact 状态(Phase 2,16-架构迭代路线图 §7.3)。
// 单 conversation 单行;CompactContent 为空表示尚未压缩过。
type ConversationContextState struct {
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// ConversationID 唯一:一个对话一份工作集状态。
	ConversationID int64 `gorm:"uniqueIndex;not null"`
	// CompactContent 当前 History Compact(Conversation Working State);NULL=未压缩过。
	CompactContent *string `gorm:"type:text"`
	// CompactUntilMessageID <= 此 Message ID 的消息已进入 Compact(水位);
	// Context 查询 = Compact + WHERE id > 此值。
	CompactUntilMessageID int64 `gorm:"not null;default:0"`
	CompactVersion        int   `gorm:"not null;default:0"` // 每次压缩 +1
	CompactCount          int   `gorm:"not null;default:0"` // 历史压缩总次数(观测 Rebase 必要性,§7.5)
	TokenCount            int   `gorm:"not null;default:0"` // 当前 Compact 估算 token 数
	UpdatedAt             time.Time
}

// TableName 指定表名
func (ConversationContextState) TableName() string {
	return "conversation_context_state"
}
