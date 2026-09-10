package memory

import "time"

// MemoryMessageLink 记忆 ↔ 消息多对多溯源映射(M5.2,12-记忆系统技术方案 §7.3):
// 一条提炼往往源自多轮交叉信息(单值 source_message_id 会失真),一条消息也可能贡献多条记忆。
// manual 记忆无映射;auto 记忆建链时校验消息必须落在本次沉淀区间内。
type MemoryMessageLink struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	MemoryID  int64     `gorm:"not null;uniqueIndex:idx_memory_message;index"`
	MessageID int64     `gorm:"not null;uniqueIndex:idx_memory_message"`
	CreatedAt time.Time `gorm:"not null"`
}

func (MemoryMessageLink) TableName() string {
	return "memory_message_links"
}

// NewMemoryMessageLinks 批量构造映射行(去重 + 排除零值,幂等写入交给唯一索引)。
func NewMemoryMessageLinks(memoryID int64, messageIDs []int64) []MemoryMessageLink {
	seen := make(map[int64]bool, len(messageIDs))
	links := make([]MemoryMessageLink, 0, len(messageIDs))
	for _, mid := range messageIDs {
		if mid <= 0 || seen[mid] {
			continue
		}
		seen[mid] = true
		links = append(links, MemoryMessageLink{
			MemoryID:  memoryID,
			MessageID: mid,
			CreatedAt: time.Now(),
		})
	}
	return links
}
