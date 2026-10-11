// Package user 仓储:用户域各表。
// larkcli_auth_repo:用户级 lark-cli 授权(19 号 §3)——按 user_id 一行,Upsert 覆盖式。
package user

import (
	"gorm.io/gorm"

	userdomain "omnibot/internal/domain/user"
)

// LarkCLIAuthRepo 用户级 lark-cli 授权仓储。
type LarkCLIAuthRepo interface {
	GetByUserID(userID int64) (*userdomain.LarkCLIAuth, error)
	Upsert(row *userdomain.LarkCLIAuth) error
	Update(row *userdomain.LarkCLIAuth) error
	Delete(userID int64) error
}

type larkCLIAuthRepo struct{ db *gorm.DB }

func NewLarkCLIAuthRepo(db *gorm.DB) LarkCLIAuthRepo { return &larkCLIAuthRepo{db: db} }

func (r *larkCLIAuthRepo) GetByUserID(userID int64) (*userdomain.LarkCLIAuth, error) {
	var row userdomain.LarkCLIAuth
	if err := r.db.Where("user_id = ?", userID).First(&row).Error; err != nil {
		return nil, err // gorm.ErrRecordNotFound 原样上抛,service 层做兼容映射
	}
	return &row, nil
}

func (r *larkCLIAuthRepo) Upsert(row *userdomain.LarkCLIAuth) error {
	var existing userdomain.LarkCLIAuth
	err := r.db.Where("user_id = ?", row.UserID).First(&existing).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return r.db.Create(row).Error
		}
		return err
	}
	row.ID = existing.ID
	row.CreatedAt = existing.CreatedAt
	return r.db.Model(&existing).Select("*").Updates(row).Error
}

func (r *larkCLIAuthRepo) Update(row *userdomain.LarkCLIAuth) error {
	return r.db.Model(&userdomain.LarkCLIAuth{}).Where("user_id = ?", row.UserID).
		Select("*").Updates(row).Error
}

func (r *larkCLIAuthRepo) Delete(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&userdomain.LarkCLIAuth{}).Error
}
