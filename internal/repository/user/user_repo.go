package user

import (
	"gorm.io/gorm"
	"omnibot/internal/domain/user"
)

// UserRepository 用户仓储接口
type UserRepository interface {
	Create(u *user.User) error
	GetByID(id int64) (*user.User, error)
	GetByPhone(phone string) (*user.User, error)
	Update(u *user.User) error
}

// GormUserRepository GORM 实现
type GormUserRepository struct {
	db *gorm.DB
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db *gorm.DB) UserRepository {
	return &GormUserRepository{db: db}
}

func (r *GormUserRepository) Create(u *user.User) error {
	return r.db.Create(u).Error
}

func (r *GormUserRepository) GetByID(id int64) (*user.User, error) {
	var u user.User
	err := r.db.First(&u, id).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *GormUserRepository) GetByPhone(phone string) (*user.User, error) {
	var u user.User
	err := r.db.Where("phone = ?", phone).First(&u).Error
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *GormUserRepository) Update(u *user.User) error {
	return r.db.Save(u).Error
}
