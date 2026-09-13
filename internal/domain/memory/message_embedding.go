package memory

import "time"

// MessageEmbedding 消息级向量(M7 中期记忆,§10.4):中期 = 原文本身,靠向量直达检索。
// 不存 content 副本——原文在 messages,命中后回表取,避免双写不一致。
type MessageEmbedding struct {
	MessageID      int64     `gorm:"primaryKey"`        // 一消息一向量;重嵌入 = 覆盖
	UserID         int64     `gorm:"not null;index"`    // 检索按用户全载余弦(§10.7,语料过万再切 pgvector)
	Role           string    `gorm:"size:20;not null"`  // user/assistant 冗余小列,免回表即可渲染
	Embedding      []float32 `gorm:"serializer:json"`   // JSON 向量列,与 memories 同款
	EmbeddingModel string    `gorm:"size:100;not null"` // 生成向量的模型标识,检索只比同模型向量(§6.3)
	CreatedAt      time.Time `gorm:"not null"`
}

func (MessageEmbedding) TableName() string { return "message_embeddings" }

// EmbeddingWatermark 消息嵌入水位(M7 §10.4):单用户单行,独立于 digest_watermarks——
// 嵌入失败不阻塞沉淀,沉淀失败不回退嵌入水位。
type EmbeddingWatermark struct {
	UserID            int64     `gorm:"primaryKey"`
	LastEmbeddedMsgID int64     // 已嵌入到的最后一条 messages.id,0=尚未嵌入(首轮回填存量)
	UpdatedAt         time.Time `gorm:"not null"`
}

func (EmbeddingWatermark) TableName() string { return "embedding_watermarks" }
