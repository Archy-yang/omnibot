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

// WechatAccountRepository 微信账号仓储接口
type WechatAccountRepository interface {
	Create(account *user.WechatAccount) error
	GetByOpenID(openID string) (*user.WechatAccount, error)
	GetByUnionID(unionID string) (*user.WechatAccount, error)
	Update(account *user.WechatAccount) error
}

// UserChannelRepository 用户渠道仓储接口
type UserChannelRepository interface {
	Create(uc *user.UserChannel) error
	GetByChannel(channelType, channelUserID string) (*user.UserChannel, error)
	GetByUserID(userID int64) ([]*user.UserChannel, error)
}

// UserService 用户服务
type UserService struct {
	userRepo    UserRepository
	wechatRepo  WechatAccountRepository
	channelRepo UserChannelRepository
}

// NewUserService 创建用户服务
func NewUserService(userRepo UserRepository, wechatRepo WechatAccountRepository, channelRepo UserChannelRepository) *UserService {
	return &UserService{
		userRepo:    userRepo,
		wechatRepo:  wechatRepo,
		channelRepo: channelRepo,
	}
}

// GetOrCreateByOpenID 根据 OpenID 获取或创建用户
// 返回: 用户, 是否新创建, 错误
func (s *UserService) GetOrCreateByOpenID(openID string) (*user.User, bool, error) {
	// 1. 查找微信账号
	account, err := s.wechatRepo.GetByOpenID(openID)
	if err == nil && account != nil {
		// 找到微信账号，获取对应用户
		u, err := s.userRepo.GetByID(account.UserID)
		if err != nil {
			return nil, false, err
		}
		return u, false, nil
	}

	// 2. 微信账号不存在，创建新用户和微信账号
	if err != gorm.ErrRecordNotFound {
		return nil, false, err
	}

	// 创建用户
	u := user.NewUser()
	if err := s.userRepo.Create(u); err != nil {
		return nil, false, err
	}

	// 创建微信账号关联
	account = user.NewWechatAccount(u.ID, openID)
	if err := s.wechatRepo.Create(account); err != nil {
		return nil, false, err
	}

	return u, true, nil
}

// GetOrCreateByChannel 根据渠道获取或创建用户
// 返回: 用户, 渠道关联, 是否新创建, 错误
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
