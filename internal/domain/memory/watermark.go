package memory

import "time"

// DigestWatermark 摘要管线水位(12-记忆系统技术方案 §4.3):单用户单行,记录已处理到的 messages.id。
// 显式水位而非从 digests 派生 max(to_message_id):删除纪要重算时水位不应倒退。
type DigestWatermark struct {
	UserID          int64     `gorm:"primaryKey"` // 主键即 user_id,单行语义
	LastDigestMsgID int64     // 摘要管线已处理到的最后一条 messages.id,0=尚未处理过
	UpdatedAt       time.Time `gorm:"not null"`
}

func (DigestWatermark) TableName() string {
	return "digest_watermarks"
}
