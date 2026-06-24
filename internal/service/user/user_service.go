package user

import (
	"gorm.io/gorm"
	"omnibot/internal/domain/user"
)

// UserRepository 用户仓储接口
type UserRepository interface {
	Create(u *user.User) error
	GetByID(id int64) (*user.User, error)
	Update(u *user.User) error
}

// UserChannelRepository 用户渠道仓储接口
type UserChannelRepository interface {
	Create(uc *user.UserChannel) error
	GetByChannel(channelType, channelUserID string) (*user.UserChannel, error)
	GetByUserID(userID int64) ([]*user.UserChannel, error)
}

// UserService 用户服务
//
// v1.8 起 UserChannels 是唯一身份解析模型——微信 / Web / 飞书都走
// GetOrCreateByChannel(channelType, channelUserID)。原 WechatAccount 双轨
// 已删除。
type UserService struct {
	userRepo    UserRepository
	channelRepo UserChannelRepository
}

// NewUserService 创建用户服务
func NewUserService(userRepo UserRepository, channelRepo UserChannelRepository) *UserService {
	return &UserService{
		userRepo:    userRepo,
		channelRepo: channelRepo,
	}
}

// GetOrCreateByChannel 根据渠道获取或创建用户
// 返回: 用户, 渠道关联, 是否新创建, 错误
//
// 三入口(微信 channelType="wechat" / Web channelType="web" / 飞书 channelType="feishu")
// 共用此方法,user_channels 唯一索引按 (channel_type, channel_user_id) 隔离。
func (s *UserService) GetOrCreateByChannel(channelType, channelUserID string) (*user.User, *user.UserChannel, bool, error) {
	// 1. 查找渠道关联
	uc, err := s.channelRepo.GetByChannel(channelType, channelUserID)
	if err == nil && uc != nil {
		// 找到渠道关联，获取对应用户
		u, err := s.userRepo.GetByID(uc.UserID)
		if err != nil {
			return nil, nil, false, err
		}
		return u, uc, false, nil
	}

	// 2. 渠道关联不存在，创建新用户和渠道关联
	if err != gorm.ErrRecordNotFound {
		return nil, nil, false, err
	}

	// 创建用户
	u := user.NewUser()
	if err := s.userRepo.Create(u); err != nil {
		return nil, nil, false, err
	}

	// 创建渠道关联
	uc = user.NewUserChannel(u.ID, channelType, channelUserID)
	if err := s.channelRepo.Create(uc); err != nil {
		return nil, nil, false, err
	}

	return u, uc, true, nil
}
