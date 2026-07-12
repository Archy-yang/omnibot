package user

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"omnibot/internal/domain/user"
)

// FeishuBindCodeRepository 飞书绑定码仓储接口(v2.2)
type FeishuBindCodeRepository interface {
	// Upsert 按 user_id 覆盖写入;同账号重新生成码时旧码自然作废(PRD 4.1)
	Upsert(code *user.FeishuBindCode) error
	GetByCode(code string) (*user.FeishuBindCode, error)
	GetByUserID(userID int64) (*user.FeishuBindCode, error)
	// DeleteByUserID 绑定成功后删码(幂等)
	DeleteByUserID(userID int64) error
}

// GormFeishuBindCodeRepository GORM 实现
type GormFeishuBindCodeRepository struct {
	db *gorm.DB
}

// NewFeishuBindCodeRepository 创建仓储
func NewFeishuBindCodeRepository(db *gorm.DB) FeishuBindCodeRepository {
	return &GormFeishuBindCodeRepository{db: db}
}

func (r *GormFeishuBindCodeRepository) Upsert(code *user.FeishuBindCode) error {
	// 冲突列 user_id -> 覆盖整行(code/expires_at/updated_at 都更新)
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"code", "expires_at", "updated_at"}),
	}).Create(code).Error
}

func (r *GormFeishuBindCodeRepository) GetByCode(code string) (*user.FeishuBindCode, error) {
	var c user.FeishuBindCode
	err := r.db.Where("code = ?", code).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *GormFeishuBindCodeRepository) GetByUserID(userID int64) (*user.FeishuBindCode, error) {
	var c user.FeishuBindCode
	err := r.db.Where("user_id = ?", userID).First(&c).Error
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (r *GormFeishuBindCodeRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&user.FeishuBindCode{}).Error
}
