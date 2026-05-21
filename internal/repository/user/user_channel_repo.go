package user

import (
	"errors"
	"time"

	"gorm.io/gorm"

	domainuser "omnibot/internal/domain/user"
)

// UserChannelRepository 用户通道仓储接口
type UserChannelRepository interface {
	Create(uc *domainuser.UserChannel) error
	GetByID(id int64) (*domainuser.UserChannel, error)
	GetByUserID(userID int64) ([]*domainuser.UserChannel, error)
	GetByChannel(channelType string, channelUserID string) (*domainuser.UserChannel, error)
	Update(uc *domainuser.UserChannel) error
	Delete(id int64) error
}

// UserChannelRepositoryImpl 用户通道仓储实现
type UserChannelRepositoryImpl struct {
	db *gorm.DB
}

// Make sure UserChannelRepositoryImpl implements UserChannelRepository
var _ UserChannelRepository = (*UserChannelRepositoryImpl)(nil)

// NewUserChannelRepository 创建用户通道仓储
func NewUserChannelRepository(db *gorm.DB) UserChannelRepository {
	return &UserChannelRepositoryImpl{db: db}
}

// Create 创建用户通道关联
func (r *UserChannelRepositoryImpl) Create(uc *domainuser.UserChannel) error {
	return r.db.Create(uc).Error
}

// GetByID 根据 ID 获取
func (r *UserChannelRepositoryImpl) GetByID(id int64) (*domainuser.UserChannel, error) {
	var uc domainuser.UserChannel
	err := r.db.First(&uc, id).Error
	if err != nil {
		return nil, err
	}
	return &uc, nil
}

// GetByUserID 获取用户的所有通道关联
func (r *UserChannelRepositoryImpl) GetByUserID(userID int64) ([]*domainuser.UserChannel, error) {
	var ucs []*domainuser.UserChannel
	err := r.db.Where("user_id = ?", userID).Find(&ucs).Error
	if err != nil {
		return nil, err
	}
	return ucs, nil
}

// GetByChannel 根据通道类型和通道用户 ID 获取
func (r *UserChannelRepositoryImpl) GetByChannel(channelType string, channelUserID string) (*domainuser.UserChannel, error) {
	var uc domainuser.UserChannel
	err := r.db.Where("channel_type = ? AND channel_user_id = ?", channelType, channelUserID).
		First(&uc).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &uc, nil
}

// Update 更新用户通道关联
func (r *UserChannelRepositoryImpl) Update(uc *domainuser.UserChannel) error {
	uc.UpdatedAt = time.Now()
	return r.db.Save(uc).Error
}

// Delete 删除用户通道关联（软删除，实际只做标记）
func (r *UserChannelRepositoryImpl) Delete(id int64) error {
	return r.db.Delete(&domainuser.UserChannel{}, id).Error
}
