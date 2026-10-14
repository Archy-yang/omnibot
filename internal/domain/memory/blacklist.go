package memory

import "time"

// MemoryBlacklist 沉淀黑名单(22号 §5):用户禁止长期沉淀的关键词/短语。
// 语义=包含匹配(大小写不敏感);拦三处:沉淀管线/注入/检索;管理视图(Journal)不过滤。
type MemoryBlacklist struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID    int64     `gorm:"uniqueIndex:idx_user_pattern,priority:1;not null" json:"-"` // 归属字段不外泄
	Pattern   string    `gorm:"uniqueIndex:idx_user_pattern,priority:2;size:128;not null" json:"pattern"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

func (MemoryBlacklist) TableName() string { return "memory_blacklists" }
