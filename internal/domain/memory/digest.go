package memory

import "time"

// 纪要状态(12-记忆系统技术方案 §4.2)
const (
	DigestStatusActive     = "active"     // 生效中,参与检索
	DigestStatusSuperseded = "superseded" // 已被重算摘要取代,保留可追溯不参与检索
)

// ConversationDigest 对话纪要(中期记忆):某段对话的主题+结论概括,带溯源区间。
type ConversationDigest struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	UserID         int64     `gorm:"index;not null"`
	Summary        string    `gorm:"type:text;not null"` // 纪要正文
	Embedding      []float32 `gorm:"serializer:json"`    // JSON 向量列,NULL=未嵌入
	EmbeddingModel string    `gorm:"size:100"`
	FromMessageID  int64     `gorm:"index;not null"` // 覆盖区间 [From, To] → messages.id(溯源)
	ToMessageID    int64     `gorm:"index;not null"`
	MsgCount       int       // 区间消息数
	Status         string    `gorm:"size:20;not null;default:active;index"`
	CreatedAt      time.Time `gorm:"not null"`
}

func (ConversationDigest) TableName() string {
	return "conversation_digests"
}

// NewConversationDigest 创建一条纪要(Status=active,区间即本次摘要处理范围)。
func NewConversationDigest(userID int64, summary string, fromMessageID, toMessageID int64, msgCount int) *ConversationDigest {
	return &ConversationDigest{
		UserID:        userID,
		Summary:       summary,
		FromMessageID: fromMessageID,
		ToMessageID:   toMessageID,
		MsgCount:      msgCount,
		Status:        DigestStatusActive,
		CreatedAt:     time.Now(),
	}
}
