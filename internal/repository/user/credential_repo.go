package user

import (
	"gorm.io/gorm"

	"omnibot/internal/domain/user"
)

// CredentialRepository 用户凭据仓储接口
type CredentialRepository interface {
	Create(cred *user.UserCredential) error
	GetByUserID(userID int64) (*user.UserCredential, error)
	Update(cred *user.UserCredential) error
}

// GormCredentialRepository GORM 实现
type GormCredentialRepository struct {
	db *gorm.DB
}

// NewCredentialRepository 创建仓储
func NewCredentialRepository(db *gorm.DB) CredentialRepository {
	return &GormCredentialRepository{db: db}
}

func (r *GormCredentialRepository) Create(cred *user.UserCredential) error {
	return r.db.Create(cred).Error
}

func (r *GormCredentialRepository) GetByUserID(userID int64) (*user.UserCredential, error) {
	var cred user.UserCredential
	err := r.db.Where("user_id = ?", userID).First(&cred).Error
	if err != nil {
		return nil, err
	}
	return &cred, nil
}

func (r *GormCredentialRepository) Update(cred *user.UserCredential) error {
	return r.db.Save(cred).Error
}
