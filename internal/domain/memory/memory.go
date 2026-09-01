package memory

import "time"

// 记忆来源(12-记忆系统技术方案 §4.1)
const (
	MemorySourceManual = "manual" // 用户显式交代(#记住 / Web 创建)
	MemorySourceAuto   = "auto"   // 沉淀管线自动提取
)

type Memory struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	UserID          int64     `gorm:"index;not null"`
	Content         string    `gorm:"type:text;not null"`
	Source          string    `gorm:"size:20;not null;default:manual"` // manual/auto
	SourceMessageID *int64    // 溯源:来源消息 ID(manual 可为 NULL)
	Embedding       []float32 `gorm:"serializer:json"` // JSON 向量列,SQLite/PG 通吃;NULL=未嵌入(检索走子串降级)
	EmbeddingModel  string    `gorm:"size:100"`        // 生成向量的模型标识,检索只比同模型向量(§6.3)
	Category        string    `gorm:"size:50"`         // 预留列,本期恒空
	Importance      int       // 预留列,本期恒 0
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (Memory) TableName() string {
	return "memories"
}

// NewMemory 创建显式记忆(Source=manual,无溯源指针)。
func NewMemory(userID int64, content string) *Memory {
	now := time.Now()
	return &Memory{
		UserID:    userID,
		Content:   content,
		Source:    MemorySourceManual,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// NewAutoMemory 创建自动沉淀记忆(Source=auto,带来源消息指针)。
func NewAutoMemory(userID int64, content string, sourceMessageID *int64) *Memory {
	now := time.Now()
	return &Memory{
		UserID:          userID,
		Content:         content,
		Source:          MemorySourceAuto,
		SourceMessageID: sourceMessageID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
