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
	// GetByIDs 按 id 集合取消息原文(中期记忆命中后回表,M7 §10.6;id 无序返回,缺失跳过)。
	GetByIDs(ids []int64) ([]*conversation.Message, error)
	// GetByTurnID 取某 Turn 的全部消息(id 升序;chunk 按 turn 重建时取全量,含早于水位的部分)。
	GetByTurnID(userID int64, turnID int64) ([]*conversation.Message, error)
	// GetRecentByUserIDAfter 取 id > afterID 的最近 limit 条(倒序取再反转,id 升序返回)。
	// Phase 2(16-架构迭代路线图 §7.3):Context 尾窗从"最近 N 条"改为"compact 水位之后"。
	GetRecentByUserIDAfter(userID int64, afterID int64, limit int) ([]*conversation.Message, error)
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

// GetByIDs 按 id 集合取消息原文(中期记忆命中后回表,M7 §10.6;缺失 id 跳过)。
func (r *messageRepository) GetByIDs(ids []int64) ([]*conversation.Message, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var messages []*conversation.Message
	err := r.db.Where("id IN ?", ids).Find(&messages).Error
	return messages, err
}

// GetRecentByUserIDAfter 取 id > afterID 的最近 limit 条,反转后按 id 升序返回。
// afterID 为 0 时等价于 GetRecentByUserID(全历史起步)。
func (r *messageRepository) GetRecentByUserIDAfter(userID int64, afterID int64, limit int) ([]*conversation.Message, error) {
	var messages []*conversation.Message
	err := r.db.Where("user_id = ? AND id > ?", userID, afterID).
		Order("id DESC").
		Limit(limit).
		Find(&messages).Error
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	return messages, nil
}

// GetByTurnID 取某 Turn 的全部消息(id 升序)。chunk 按 turn 重建时需要全量消息,
// 包括早于 chunk 水位的部分(迟到 report 的 turn 已在上轮处理过)。
func (r *messageRepository) GetByTurnID(userID int64, turnID int64) ([]*conversation.Message, error) {
	var messages []*conversation.Message
	err := r.db.Where("user_id = ? AND turn_id = ?", userID, turnID).
		Order("id ASC").
		Find(&messages).Error
	if err != nil {
		return nil, err
	}
	return messages, nil
}
