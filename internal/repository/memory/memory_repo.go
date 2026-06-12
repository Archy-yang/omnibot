package memory

import (
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

type MemoryRepository interface {
	Create(memory *memorydomain.Memory) error
	ListByUserID(userID int64) ([]*memorydomain.Memory, error)
	DeleteByUserID(userID int64) error
	GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error)
	GetByID(id int64, userID int64) (*memorydomain.Memory, error)
	DeleteByID(id int64, userID int64) (bool, error)
	UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error)
}

type memoryRepository struct {
	db *gorm.DB
}

func NewMemoryRepository(db *gorm.DB) MemoryRepository {
	return &memoryRepository{db: db}
}

func (r *memoryRepository) Create(memory *memorydomain.Memory) error {
	return r.db.Create(memory).Error
}

func (r *memoryRepository) ListByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

func (r *memoryRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&memorydomain.Memory{}).Error
}

func (r *memoryRepository) GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).
		Find(&memories).Error
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(memories)-1; i < j; i, j = i+1, j-1 {
		memories[i], memories[j] = memories[j], memories[i]
	}

	return memories, nil
}

func (r *memoryRepository) GetByID(id int64, userID int64) (*memorydomain.Memory, error) {
	var memory memorydomain.Memory
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&memory).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &memory, err
}

func (r *memoryRepository) DeleteByID(id int64, userID int64) (bool, error) {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&memorydomain.Memory{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *memoryRepository) UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error) {
	result := r.db.Model(&memorydomain.Memory{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"content":    content,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return r.GetByID(id, userID)
}
