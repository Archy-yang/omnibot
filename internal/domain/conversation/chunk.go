package conversation

import (
	"time"
)

// ConversationChunk 对话召回块(Phase 3,16-架构迭代路线图 §9):
// 以 Complete Turn 为索引粒度的向量检索单元(§9.2——"所以还是第二种?"这类单条消息
// 没有完整语义,不独立成 chunk);长 Turn 按 token 切分(§9.5),每片保留 turn 归属与消息区间。
//
// chunk 是派生数据(可随时从 messages 重建),Content 是区间原文的格式化副本,允许冗余。
type ConversationChunk struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"index;not null"`
	ConversationID int64     `gorm:"index;not null"`
	TurnID         int64     `gorm:"index;not null"` // 所属逻辑 Turn(Phase 1 建立)
	Seq            int       `gorm:"not null"`       // turn 内分片序号(0 起),长 Turn 切分后保序
	StartMessageID int64     `gorm:"not null"`       // 覆盖的消息区间 [start, end](回表展开用)
	EndMessageID   int64     `gorm:"not null"`
	Content        string    `gorm:"type:text;not null"` // "[user] ..." 格式化原文(与 Compact 输入同款格式)
	TokenCount     int       `gorm:"not null"`
	Embedding      []float32 `gorm:"serializer:json"` // JSON 向量列,与 memories/message_embeddings 同款
	EmbeddingModel string    `gorm:"size:100;not null"`
	CreatedAt      time.Time `gorm:"not null"`
	UpdatedAt      time.Time
}

// TableName 指定表名
func (ConversationChunk) TableName() string {
	return "conversation_chunks"
}

// ChunkEmbeddingWatermark chunk 构建水位(Phase 3):单用户单行,独立于消息嵌入水位。
// 记录已按 turn 重建过 chunk 的最大消息 id。
type ChunkEmbeddingWatermark struct {
	UserID           int64     `gorm:"primaryKey"`
	LastProcessedMsgID int64   // 已处理到的最后一条 messages.id,0=尚未处理(首轮回填存量)
	UpdatedAt        time.Time `gorm:"not null"`
}

// TableName 指定表名
func (ChunkEmbeddingWatermark) TableName() string {
	return "chunk_embedding_watermarks"
}
