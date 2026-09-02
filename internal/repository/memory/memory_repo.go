package memory

import (
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

type MemoryRepository interface {
	Create(memory *memorydomain.Memory) error
	ListByUserID(userID int64) ([]*memorydomain.Memory, error)
	// ListManualByUserID 仅手动记忆(id 升序)——常驻注入用(注入分层:手动常驻,自动走工具)。
	ListManualByUserID(userID int64) ([]*memorydomain.Memory, error)
	// CountByUserIDAndSource 按来源计数(注入的存在性提示行用)。
	CountByUserIDAndSource(userID int64, source string) (int64, error)
	DeleteByUserID(userID int64) error
	// DeleteByUserIDAndSource 按来源清空(记忆抽屉双 tab 各清各的,注入分层)。
	DeleteByUserIDAndSource(userID int64, source string) error
	GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error)
	GetByID(id int64, userID int64) (*memorydomain.Memory, error)
	DeleteByID(id int64, userID int64) (bool, error)
	UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error)
	// UpdateContentEmbeddingByID 沉淀管线疑似冲突时原位更新(内容+向量+模型标记,§7.3)。
	UpdateContentEmbeddingByID(id int64, userID int64, content string, embedding []float32, embeddingModel string) error
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

// ListManualByUserID 仅手动记忆(id 升序)——常驻注入用。
func (r *memoryRepository) ListManualByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ? AND source = ?", userID, memorydomain.MemorySourceManual).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

// CountByUserIDAndSource 按来源计数。
func (r *memoryRepository) CountByUserIDAndSource(userID int64, source string) (int64, error) {
	var count int64
	err := r.db.Model(&memorydomain.Memory{}).
		Where("user_id = ? AND source = ?", userID, source).
		Count(&count).Error
	return count, err
}

func (r *memoryRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&memorydomain.Memory{}).Error
}

// DeleteByUserIDAndSource 按来源清空。
func (r *memoryRepository) DeleteByUserIDAndSource(userID int64, source string) error {
	return r.db.Where("user_id = ? AND source = ?", userID, source).
		Delete(&memorydomain.Memory{}).Error
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

// UpdateContentEmbeddingByID 沉淀管线疑似冲突时原位更新(内容+向量+模型标记,§7.3)。
// embedding 为 nil 时只更新内容并清空向量(内容变了旧向量不可再用)。
// 取行再 Save:GORM 的 map Updates 不走 serializer(JSON 向量列),struct Save 才会。
func (r *memoryRepository) UpdateContentEmbeddingByID(id int64, userID int64, content string, embedding []float32, embeddingModel string) error {
	var mem memorydomain.Memory
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&mem).Error; err != nil {
		return err
	}
	mem.Content = content
	mem.Embedding = embedding
	mem.EmbeddingModel = embeddingModel
	mem.UpdatedAt = time.Now()
	return r.db.Save(&mem).Error
}
