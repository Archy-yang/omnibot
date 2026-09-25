package conversation

import (
	"time"
)

// ConversationStatusActive 进行中的长期对话(单用户单 active,产品约束,应用层保证)
const ConversationStatusActive = "active"

// Conversation 用户与 Agent 的长期对话空间(16-架构迭代路线图 §5.2)。
// 产品层面一个 (user_id, agent_id) 只有一个 active conversation;数据库不建唯一约束
// (归档态允许多行),由 EnsureActiveConversation 应用层保证。
type Conversation struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"index;not null"`
	AgentID   int64     `gorm:"index;not null"` // 关联 agents 表(全场景预留,目前恒为 main)
	Status    string    `gorm:"size:20;not null;index"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time
}

// TableName 指定表名
func (Conversation) TableName() string {
	return "conversations"
}

// NewConversation 创建一个 active 对话空间
func NewConversation(userID, agentID int64) *Conversation {
	now := time.Now()
	return &Conversation{
		UserID:    userID,
		AgentID:   agentID,
		Status:    ConversationStatusActive,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// ConversationTurn 一次用户意图的逻辑执行单元(§5.2)。
// 由 User Message 开启;其后的 assistant 回复、delegate 的任务、异步 Report 都归属该 Turn。
// 薄表无业务字段,是 Compact 水位(Phase 2)与 Recall Chunk(Phase 3)的身份锚点。
type ConversationTurn struct {
	ID             int64     `gorm:"primaryKey;autoIncrement"`
	ConversationID int64     `gorm:"index;not null"`
	UserID         int64     `gorm:"index;not null"`
	CreatedAt      time.Time `gorm:"not null"`
}

// TableName 指定表名
func (ConversationTurn) TableName() string {
	return "conversation_turns"
}

// NewConversationTurn 在指定对话内开启一个新 Turn
func NewConversationTurn(conversationID, userID int64) *ConversationTurn {
	return &ConversationTurn{
		ConversationID: conversationID,
		UserID:         userID,
		CreatedAt:      time.Now(),
	}
}
