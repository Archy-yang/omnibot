package chat

import (
	"wechat-intelligent-bot/internal/domain/conversation"

	"gorm.io/gorm"
)

// MessageRepository 消息仓储接口
type MessageRepository interface {
	Create(msg *conversation.Message) error
	GetRecentByUserID(userID int64, limit int) ([]*conversation.Message, error)
	ExistsByMsgID(msgID string) (bool, error)
}

type messageRepository struct {
	db *gorm.DB
}

// NewMessageRepository 创建消息仓储
func NewMessageRepository(db *gorm.DB) MessageRepository {
	return &messageRepository{db: db}
}

// Create 创建消息
func (r *messageRepository) Create(msg *conversation.Message) error {
	return r.db.Create(msg).Error
}

// GetRecentByUserID 获取用户最近的消息，按时间正序排列（旧的在前，新的在后）
func (r *messageRepository) GetRecentByUserID(userID int64, limit int) ([]*conversation.Message, error) {
	var messages []*conversation.Message

	// 先按时间倒序取最近 limit 条，然后反转成正序
	err := r.db.Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).
		Find(&messages).Error

	if err != nil {
		return nil, err
	}

	// 反转数组为正序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// ExistsByMsgID 检查微信消息ID是否已存在（用于去重）
func (r *messageRepository) ExistsByMsgID(msgID string) (bool, error) {
	var count int64
	err := r.db.Model(&conversation.Message{}).
		Where("msg_id = ?", msgID).
		Count(&count).Error
	return count > 0, err
}
