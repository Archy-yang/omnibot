package memory

import (
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

// MatterRepository 事项仓储(M6,助理人视角)。
type MatterRepository interface {
	// UpsertByTitle 同 (user_id,title) 覆写 StateDesc/Status/LastMsgID;不存在则创建。
	// 语义:事项状态是"覆写式"认知更新,不是追加。
	UpsertByTitle(m *memorydomain.Matter) error
	// GetByTitle 按标题精确取(对账时 fact 挂靠用);不存在返回 nil。
	GetByTitle(userID int64, title string) (*memorydomain.Matter, error)
	// ListActiveByUserID 活跃事项(updated_at 倒序;世界观快照用)。
	ListActiveByUserID(userID int64) ([]*memorydomain.Matter, error)
}

type matterRepository struct {
	db *gorm.DB
}

func NewMatterRepository(db *gorm.DB) MatterRepository {
	return &matterRepository{db: db}
}

func (r *matterRepository) UpsertByTitle(m *memorydomain.Matter) error {
	var existing memorydomain.Matter
	err := r.db.Where("user_id = ? AND title = ?", m.UserID, m.Title).First(&existing).Error
	if err == nil {
		existing.StateDesc = m.StateDesc
		existing.Status = m.Status
		existing.LastMsgID = m.LastMsgID
		existing.Embedding = m.Embedding // 状态变了向量要跟着换(M6.2 检索正确性)
		existing.EmbeddingModel = m.EmbeddingModel
		existing.UpdatedAt = time.Now()
		return r.db.Save(&existing).Error
	}
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(m).Error
	}
	return err
}

func (r *matterRepository) GetByTitle(userID int64, title string) (*memorydomain.Matter, error) {
	var m memorydomain.Matter
	err := r.db.Where("user_id = ? AND title = ?", userID, title).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *matterRepository) ListActiveByUserID(userID int64) ([]*memorydomain.Matter, error) {
	var matters []*memorydomain.Matter
	err := r.db.Where("user_id = ? AND status = ?", userID, memorydomain.MatterStatusActive).
		Order("updated_at DESC").
		Find(&matters).Error
	return matters, err
}
