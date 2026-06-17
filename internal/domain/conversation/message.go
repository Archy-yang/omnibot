package conversation

import (
	"time"
)

// Message 角色常量
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
)

// Message 对话消息实体
type Message struct {
	ID        int64      `gorm:"primaryKey;autoIncrement"`
	UserID    int64      `gorm:"index;not null"`                // 用户 ID
	Role      string     `gorm:"size:20;not null;index"`        // 消息角色：user/assistant/system/tool
	Content   string     `gorm:"type:text;not null"`             // 消息内容
	MsgID     *string    `gorm:"size:100;uniqueIndex"`           // 微信消息 ID（用于去重，仅 user 消息有）
	CreatedAt time.Time  `gorm:"not null"`
}

// TableName 指定表名
func (Message) TableName() string {
	return "messages"
}

// NewUserMessage 创建用户消息。
// msgID 为空字符串时（如 Web 渠道），存储为 NULL，避免在 msg_id 唯一索引上发生
// 第二条空串冲突；非空时（如微信渠道）存储具体值用于消息去重。
func NewUserMessage(userID int64, content string, msgID string) *Message {
	now := time.Now()
	msg := &Message{
		UserID:    userID,
		Role:      RoleUser,
		Content:   content,
		CreatedAt: now,
	}
	if msgID != "" {
		msg.MsgID = &msgID
	}
	return msg
}

// NewAssistantMessage 创建助手消息
func NewAssistantMessage(userID int64, content string) *Message {
	now := time.Now()
	return &Message{
		UserID:    userID,
		Role:      RoleAssistant,
		Content:   content,
		MsgID:     nil,
		CreatedAt: now,
	}
}
