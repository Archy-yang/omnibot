package chat

import (
	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
)

// MessageRepository 消息仓储接口
type MessageRepository interface {
	Create(msg *conversation.Message) error
	GetRecentByUserID(userID int64, limit int) ([]*conversation.Message, error)
	GetByUserIDBefore(userID int64, beforeID int64, limit int) ([]*conversation.Message, error)
	ExistsByMsgID(msgID string) (bool, error)
	// GetLatestMessageID 用户最新一条消息 ID;无消息返回 0(12-记忆系统技术方案 §7 沉淀管线用)。
	GetLatestMessageID(userID int64) (int64, error)
	// GetRangeByUserID 返回 (afterID, toID] 区间的消息,id 升序(纪要区间即本次处理范围)。
	GetRangeByUserID(userID int64, afterID, toID int64) ([]*conversation.Message, error)
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

// GetByUserIDBefore 获取用户在指定 ID 之前的最近 limit 条消息，按时间正序排列（旧的在前）。
// 用于历史消息翻页：前端把已加载列表中最旧一条的 ID 作为 beforeID 传入，逐步往前拉。
func (r *messageRepository) GetByUserIDBefore(userID int64, beforeID int64, limit int) ([]*conversation.Message, error) {
	var messages []*conversation.Message

	err := r.db.Where("user_id = ? AND id < ?", userID, beforeID).
		Order("id DESC").
		Limit(limit).
		Find(&messages).Error

	if err != nil {
		return nil, err
	}

	// 反转为正序
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

// GetLatestMessageID 用户最新一条消息 ID;无消息返回 0(12-记忆系统技术方案 §7 沉淀管线用)。
func (r *messageRepository) GetLatestMessageID(userID int64) (int64, error) {
	var msg conversation.Message
	err := r.db.Where("user_id = ?", userID).Order("id DESC").First(&msg).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return msg.ID, nil
}

// GetRangeByUserID 返回 (afterID, toID] 区间的消息,id 升序(纪要区间即本次处理范围)。
func (r *messageRepository) GetRangeByUserID(userID int64, afterID, toID int64) ([]*conversation.Message, error) {
	var messages []*conversation.Message
	err := r.db.Where("user_id = ? AND id > ? AND id <= ?", userID, afterID, toID).
		Order("id ASC").
		Find(&messages).Error
	return messages, err
}
