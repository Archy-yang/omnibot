package user

import (
	"gorm.io/gorm"
	"wechat-intelligent-bot/internal/domain/user"
)

// WechatAccountRepository 微信账号仓储接口
type WechatAccountRepository interface {
	Create(account *user.WechatAccount) error
	GetByOpenID(openID string) (*user.WechatAccount, error)
	GetByUnionID(unionID string) (*user.WechatAccount, error)
	Update(account *user.WechatAccount) error
}

// GormWechatAccountRepository GORM 实现
type GormWechatAccountRepository struct {
	db *gorm.DB
}

// NewWechatAccountRepository 创建微信账号仓储
func NewWechatAccountRepository(db *gorm.DB) WechatAccountRepository {
	return &GormWechatAccountRepository{db: db}
}

func (r *GormWechatAccountRepository) Create(account *user.WechatAccount) error {
	return r.db.Create(account).Error
}

func (r *GormWechatAccountRepository) GetByOpenID(openID string) (*user.WechatAccount, error) {
	var account user.WechatAccount
	err := r.db.Where("open_id = ?", openID).First(&account).Error
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *GormWechatAccountRepository) GetByUnionID(unionID string) (*user.WechatAccount, error) {
	var account user.WechatAccount
	err := r.db.Where("union_id = ?", unionID).First(&account).Error
	if err != nil {
		return nil, err
	}
	return &account, nil
}

func (r *GormWechatAccountRepository) Update(account *user.WechatAccount) error {
	return r.db.Save(account).Error
}
