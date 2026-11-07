package search

// config_repository.go — 用户级联网搜索配置的持久化(32 号 §4.2)。

import (
	"errors"

	"gorm.io/gorm"

	domain "omnibot/internal/domain/search"
)

// ConfigRepository 配置持久化窄接口(service 层依赖此接口,不依赖 GORM)。
type ConfigRepository interface {
	// GetByUserID 查配置;不存在返回 (nil, nil) —— "没有配置"是正常状态,不是错误。
	GetByUserID(userID int64) (*domain.Config, error)
	Create(cfg *domain.Config) error
	Update(cfg *domain.Config) error
	Delete(userID int64) error
}

// GormConfigRepository GORM 实现。
type GormConfigRepository struct {
	db *gorm.DB
}

// NewConfigRepository 创建仓储。
func NewConfigRepository(db *gorm.DB) ConfigRepository {
	return &GormConfigRepository{db: db}
}

func (r *GormConfigRepository) GetByUserID(userID int64) (*domain.Config, error) {
	var cfg domain.Config
	err := r.db.Where("user_id = ?", userID).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *GormConfigRepository) Create(cfg *domain.Config) error {
	return r.db.Create(cfg).Error
}

func (r *GormConfigRepository) Update(cfg *domain.Config) error {
	return r.db.Save(cfg).Error
}

func (r *GormConfigRepository) Delete(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&domain.Config{}).Error
}
