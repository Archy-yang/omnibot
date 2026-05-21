package user

import (
	domainuser "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
)

// UserChannelService 用户通道服务接口
type UserChannelService interface {
	// GetOrCreateByChannel 获取或创建用户通道关联
	// 返回：(用户, 通道关联, 是否新创建, 错误)
	GetOrCreateByChannel(channelType string, channelUserID string) (*domainuser.User, *domainuser.UserChannel, bool, error)

	// GetByUserID 获取用户的所有通道
	GetByUserID(userID int64) ([]*domainuser.UserChannel, error)

	// UpdateChannel 更新通道信息
	UpdateChannel(uc *domainuser.UserChannel) error
}

// UserChannelServiceImpl 用户通道服务实现
type UserChannelServiceImpl struct {
	userRepo      repo.UserRepository
	channelRepo   repo.UserChannelRepository
}

// Make sure UserChannelServiceImpl implements UserChannelService
var _ UserChannelService = (*UserChannelServiceImpl)(nil)

// NewUserChannelService 创建用户通道服务
func NewUserChannelService(
	userRepo repo.UserRepository,
	channelRepo repo.UserChannelRepository,
) UserChannelService {
	return &UserChannelServiceImpl{
		userRepo:    userRepo,
		channelRepo: channelRepo,
	}
}

// GetOrCreateByChannel 获取或创建用户通道关联
func (s *UserChannelServiceImpl) GetOrCreateByChannel(
	channelType string,
	channelUserID string,
) (*domainuser.User, *domainuser.UserChannel, bool, error) {
	// 先查找是否已存在
	existingChannel, err := s.channelRepo.GetByChannel(channelType, channelUserID)
	if err != nil {
		return nil, nil, false, err
	}

	if existingChannel != nil {
		// 找到通道，加载用户信息
		user, err := s.userRepo.GetByID(existingChannel.UserID)
		if err != nil {
			return nil, nil, false, err
		}
		return user, existingChannel, false, nil
	}

	// 不存在，创建新用户和通道关联
	user := domainuser.NewUser()
	err = s.userRepo.Create(user)
	if err != nil {
		return nil, nil, false, err
	}

	// 创建通道关联
	uc := domainuser.NewUserChannel(user.ID, channelType, channelUserID)
	err = s.channelRepo.Create(uc)
	if err != nil {
		return nil, nil, false, err
	}

	return user, uc, true, nil
}

// GetByUserID 获取用户的所有通道
func (s *UserChannelServiceImpl) GetByUserID(userID int64) ([]*domainuser.UserChannel, error) {
	return s.channelRepo.GetByUserID(userID)
}

// UpdateChannel 更新通道信息
func (s *UserChannelServiceImpl) UpdateChannel(uc *domainuser.UserChannel) error {
	return s.channelRepo.Update(uc)
}
