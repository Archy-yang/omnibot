package chat

import (
	"fmt"

	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ContextStateRepository Conversation Compact 状态的存取(Phase 2,§7.3)。
type ContextStateRepository interface {
	// GetByConversationID 无记录时返回 (nil, nil)——尚未压缩过的对话。
	GetByConversationID(conversationID int64) (*conversation.ConversationContextState, error)
	// Upsert 幂等写入(单 conversation 单行)。
	Upsert(state *conversation.ConversationContextState) error
}

type contextStateRepository struct {
	db *gorm.DB
}

func NewContextStateRepository(db *gorm.DB) ContextStateRepository {
	return &contextStateRepository{db: db}
}

func (r *contextStateRepository) GetByConversationID(conversationID int64) (*conversation.ConversationContextState, error) {
	var state conversation.ConversationContextState
	err := r.db.Where("conversation_id = ?", conversationID).First(&state).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("query context state: %w", err)
	}
	return &state, nil
}

func (r *contextStateRepository) Upsert(state *conversation.ConversationContextState) error {
	if err := r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "conversation_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"compact_content", "compact_until_message_id", "compact_version", "compact_count", "token_count", "updated_at"}),
	}).Create(state).Error; err != nil {
		return fmt.Errorf("upsert context state: %w", err)
	}
	return nil
}
