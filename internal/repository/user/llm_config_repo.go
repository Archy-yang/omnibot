package user

import (
	"gorm.io/gorm"
	"wechat-intelligent-bot/internal/domain/user"
)

// LLMConfigRepository LLM 配置仓储接口
type LLMConfigRepository interface {
	GetByUserID(userID int64) (*user.LLMConfig, error)
	Create(config *user.LLMConfig) error
	Update(config *user.LLMConfig) error
	Delete(userID int64) error
}

// GormLLMConfigRepository GORM 实现
type GormLLMConfigRepository struct {
	db *gorm.DB
}

// NewLLMConfigRepository 创建仓储
func NewLLMConfigRepository(db *gorm.DB) LLMConfigRepository {
	return &GormLLMConfigRepository{db: db}
}

func (r *GormLLMConfigRepository) GetByUserID(userID int64) (*user.LLMConfig, error) {
	var cfg user.LLMConfig
	err := r.db.Where("user_id = ?", userID).First(&cfg).Error
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *GormLLMConfigRepository) Create(config *user.LLMConfig) error {
	return r.db.Create(config).Error
}

func (r *GormLLMConfigRepository) Update(config *user.LLMConfig) error {
	return r.db.Save(config).Error
}

func (r *GormLLMConfigRepository) Delete(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&user.LLMConfig{}).Error
}
