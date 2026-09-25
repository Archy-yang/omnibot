package chat

import (
	"fmt"
	"time"

	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ChunkWatermarkRepository chunk 构建水位的存取(单用户单行)。
type ChunkWatermarkRepository interface {
	GetByUserID(userID int64) (*conversation.ChunkEmbeddingWatermark, error)
	Save(userID int64, lastMsgID int64) error
}

type chunkWatermarkRepository struct {
	db *gorm.DB
}

func NewChunkWatermarkRepository(db *gorm.DB) ChunkWatermarkRepository {
	return &chunkWatermarkRepository{db: db}
}

func (r *chunkWatermarkRepository) GetByUserID(userID int64) (*conversation.ChunkEmbeddingWatermark, error) {
	var wm conversation.ChunkEmbeddingWatermark
	err := r.db.Where("user_id = ?", userID).First(&wm).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return &conversation.ChunkEmbeddingWatermark{UserID: userID}, nil
		}
		return nil, fmt.Errorf("query chunk watermark: %w", err)
	}
	return &wm, nil
}

func (r *chunkWatermarkRepository) Save(userID int64, lastMsgID int64) error {
	wm := &conversation.ChunkEmbeddingWatermark{UserID: userID, LastProcessedMsgID: lastMsgID, UpdatedAt: time.Now()}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_processed_msg_id", "updated_at"}),
	}).Create(wm).Error
}

// ConversationChunkRepository chunk 的存取。chunk 是派生数据:turn 重建 = 删旧插新。
type ConversationChunkRepository interface {
	// ReplaceTurnChunks 原子替换一个 turn 的全部 chunk(先删后插,同事务)。
	ReplaceTurnChunks(turnID int64, chunks []*conversation.ConversationChunk) error
	// ListByUserID 该用户全部 chunk(turn_id,seq 升序;检索全载余弦,语料过万再切 pgvector)。
	ListByUserID(userID int64) ([]*conversation.ConversationChunk, error)
}

type conversationChunkRepository struct {
	db *gorm.DB
}

func NewConversationChunkRepository(db *gorm.DB) ConversationChunkRepository {
	return &conversationChunkRepository{db: db}
}

func (r *conversationChunkRepository) ReplaceTurnChunks(turnID int64, chunks []*conversation.ConversationChunk) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("turn_id = ?", turnID).Delete(&conversation.ConversationChunk{}).Error; err != nil {
			return fmt.Errorf("delete old chunks: %w", err)
		}
		if len(chunks) == 0 {
			return nil
		}
		return tx.Create(&chunks).Error
	})
}

func (r *conversationChunkRepository) ListByUserID(userID int64) ([]*conversation.ConversationChunk, error) {
	var out []*conversation.ConversationChunk
	err := r.db.Where("user_id = ?", userID).Order("turn_id ASC, seq ASC").Find(&out).Error
	if err != nil {
		return nil, fmt.Errorf("list chunks: %w", err)
	}
	return out, nil
}
