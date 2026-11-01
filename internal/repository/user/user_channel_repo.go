package user

import (
	"gorm.io/gorm"
	"omnibot/internal/domain/user"
)

// UserChannelRepository 用户渠道仓储接口
type UserChannelRepository interface {
	Create(uc *user.UserChannel) error
	GetByID(id int64) (*user.UserChannel, error)
	GetByChannel(channelType, channelUserID string) (*user.UserChannel, error)
	GetByUserID(userID int64) ([]*user.UserChannel, error)
	// DeleteByChannelTypeExcept 删除某渠道下除指定身份外的绑定(ClawBot 换号/换人清理)
	DeleteByChannelTypeExcept(channelType, keepChannelUserID string) (int64, error)
	Update(uc *user.UserChannel) error
	Delete(id int64) error
}

// GormUserChannelRepository GORM 实现
type GormUserChannelRepository struct {
	db *gorm.DB
}

// NewUserChannelRepository 创建用户渠道仓储
func NewUserChannelRepository(db *gorm.DB) UserChannelRepository {
	return &GormUserChannelRepository{db: db}
}

func (r *GormUserChannelRepository) Create(uc *user.UserChannel) error {
	return r.db.Create(uc).Error
}

func (r *GormUserChannelRepository) GetByChannel(channelType, channelUserID string) (*user.UserChannel, error) {
	var uc user.UserChannel
	err := r.db.Where("channel_type = ? AND channel_user_id = ?", channelType, channelUserID).First(&uc).Error
	if err != nil {
		return nil, err
	}
	return &uc, nil
}

func (r *GormUserChannelRepository) GetByUserID(userID int64) ([]*user.UserChannel, error) {
	var ucs []*user.UserChannel
	err := r.db.Where("user_id = ?", userID).Find(&ucs).Error
	if err != nil {
		return nil, err
	}
	return ucs, nil
}

// DeleteByChannelTypeExcept 删除某渠道下除指定身份外的绑定。
func (r *GormUserChannelRepository) DeleteByChannelTypeExcept(channelType, keepChannelUserID string) (int64, error) {
	res := r.db.Where("channel_type = ? AND channel_user_id <> ?", channelType, keepChannelUserID).
		Delete(&user.UserChannel{})
	return res.RowsAffected, res.Error
}

func (r *GormUserChannelRepository) Update(uc *user.UserChannel) error {
	return r.db.Save(uc).Error
}

func (r *GormUserChannelRepository) GetByID(id int64) (*user.UserChannel, error) {
	var uc user.UserChannel
	err := r.db.Where("id = ?", id).First(&uc).Error
	if err != nil {
		return nil, err
	}
	return &uc, nil
}

func (r *GormUserChannelRepository) Delete(id int64) error {
	return r.db.Delete(&user.UserChannel{}, id).Error
}
