package memory

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"

	memorydomain "omnibot/internal/domain/memory"
)

// MessageEmbeddingRepository 消息向量仓储(M7 §10.4)。
type MessageEmbeddingRepository interface {
	// UpsertBatch 批量写入,同 message_id 覆盖(重嵌入幂等);空批次静默。
	UpsertBatch(embs []*memorydomain.MessageEmbedding) error
	// ListByUserID 该用户全部消息向量(检索 Go 侧全载余弦,§10.7)。
	ListByUserID(userID int64) ([]*memorydomain.MessageEmbedding, error)
}

type messageEmbeddingRepository struct {
	db *gorm.DB
}

func NewMessageEmbeddingRepository(db *gorm.DB) MessageEmbeddingRepository {
	return &messageEmbeddingRepository{db: db}
}

func (r *messageEmbeddingRepository) UpsertBatch(embs []*memorydomain.MessageEmbedding) error {
	if len(embs) == 0 {
		return nil // GORM Create 空切片报错,显式保护
	}
	return r.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "message_id"}}, DoUpdates: clause.AssignmentColumns([]string{"role", "embedding", "embedding_model", "created_at"})}).
		Create(&embs).Error
}

func (r *messageEmbeddingRepository) ListByUserID(userID int64) ([]*memorydomain.MessageEmbedding, error) {
	var out []*memorydomain.MessageEmbedding
	if err := r.db.Where("user_id = ?", userID).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// EmbeddingWatermarkRepository 消息嵌入水位(单用户单行,独立于 digest 水位)。
type EmbeddingWatermarkRepository interface {
	GetByUserID(userID int64) (*memorydomain.EmbeddingWatermark, error)
	Save(userID int64, lastMsgID int64) error
}

type embeddingWatermarkRepository struct {
	db *gorm.DB
}

func NewEmbeddingWatermarkRepository(db *gorm.DB) EmbeddingWatermarkRepository {
	return &embeddingWatermarkRepository{db: db}
}

func (r *embeddingWatermarkRepository) GetByUserID(userID int64) (*memorydomain.EmbeddingWatermark, error) {
	var wm memorydomain.EmbeddingWatermark
	if err := r.db.Where("user_id = ?", userID).First(&wm).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return &memorydomain.EmbeddingWatermark{UserID: userID, LastEmbeddedMsgID: 0}, nil
		}
		return nil, err
	}
	return &wm, nil
}

func (r *embeddingWatermarkRepository) Save(userID int64, lastMsgID int64) error {
	wm := memorydomain.EmbeddingWatermark{UserID: userID, LastEmbeddedMsgID: lastMsgID, UpdatedAt: time.Now()}
	return r.db.Save(&wm).Error
}
