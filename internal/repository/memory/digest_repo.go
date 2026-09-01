package memory

import (
	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

// DigestRepository 对话纪要仓储(12-记忆系统技术方案 §4.2)。
type DigestRepository interface {
	Create(digest *memorydomain.ConversationDigest) error
	ListActiveByUserID(userID int64) ([]*memorydomain.ConversationDigest, error)
	MarkSuperseded(id int64, userID int64) error
	DeleteByID(id int64, userID int64) (bool, error)
}

type digestRepository struct {
	db *gorm.DB
}

func NewDigestRepository(db *gorm.DB) DigestRepository {
	return &digestRepository{db: db}
}

func (r *digestRepository) Create(digest *memorydomain.ConversationDigest) error {
	return r.db.Create(digest).Error
}

// ListActiveByUserID 按 id 升序列出生效纪要(superseded 不参与检索)。
func (r *digestRepository) ListActiveByUserID(userID int64) ([]*memorydomain.ConversationDigest, error) {
	var digests []*memorydomain.ConversationDigest
	err := r.db.Where("user_id = ? AND status = ?", userID, memorydomain.DigestStatusActive).
		Order("id ASC").
		Find(&digests).Error
	return digests, err
}

// MarkSuperseded 标记纪要被重算摘要取代(保留可追溯,不物理删除)。
func (r *digestRepository) MarkSuperseded(id int64, userID int64) error {
	return r.db.Model(&memorydomain.ConversationDigest{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", memorydomain.DigestStatusSuperseded).Error
}

func (r *digestRepository) DeleteByID(id int64, userID int64) (bool, error) {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).
		Delete(&memorydomain.ConversationDigest{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
