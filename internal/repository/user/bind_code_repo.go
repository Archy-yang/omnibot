package user

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"omnibot/internal/domain/user"
)

// BindCodeRepository 绑定码仓储接口(v2.2)
type BindCodeRepository interface {
	// Upsert 按 user_id 覆盖写入;同账号重新生成码时旧码自然作废(PRD 4.1)
	Upsert(code *user.BindCode) error
	GetByCode(code string) (*user.BindCode, error)
	GetByUserID(userID int64) (*user.BindCode, error)
	// DeleteByUserID 绑定成功后删码(幂等)
	DeleteByUserID(userID int64) error
}

// GormBindCodeRepository GORM 实现
type GormBindCodeRepository struct {
	db *gorm.DB
}

// NewBindCodeRepository 创建仓储
func NewBindCodeRepository(db *gorm.DB) BindCodeRepository {
	return &GormBindCodeRepository{db: db}
}

func (r *GormBindCodeRepository) Upsert(code *user.BindCode) error {
	// 冲突列 user_id -> 覆盖整行(code/expires_at/updated_at 都更新)
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"code", "expires_at", "updated_at"}),
	}).Create(code).Error
}

func (r *GormBindCodeRepository) GetByCode(code string) (*user.BindCode, error) {
	var c user.BindCode
	err := r.db.Where("code = ?", code).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *GormBindCodeRepository) GetByUserID(userID int64) (*user.BindCode, error) {
	var c user.BindCode
	err := r.db.Where("user_id = ?", userID).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *GormBindCodeRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&user.BindCode{}).Error
}
