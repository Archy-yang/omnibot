package user

import (
	"errors"

	domainuser "omnibot/internal/domain/user"
)

// UserRepository 用户仓储接口
type UserRepository interface {
	Create(u *domainuser.User) error
	GetByID(id int64) (*domainuser.User, error)
	Update(u *domainuser.User) error
}

// WechatAccountRepository 微信账号仓储接口（v1.2 兼容，v1.4 后废弃）
type WechatAccountRepository interface {
	Create(account *domainuser.WechatAccount) error
	GetByOpenID(openID string) (*domainuser.WechatAccount, error)
	GetByUnionID(unionID string) (*domainuser.WechatAccount, error)
	Update(account *domainuser.WechatAccount) error
}

// UserChannelRepository 用户通道仓储接口（v1.3 新增）
type UserChannelRepository interface {
	Create(uc *domainuser.UserChannel) error
	GetByID(id int64) (*domainuser.UserChannel, error)
	GetByUserID(userID int64) ([]*domainuser.UserChannel, error)
	GetByChannel(channelType string, channelUserID string) (*domainuser.UserChannel, error)
	Update(uc *domainuser.UserChannel) error
	Delete(id int64) error
}

// UserService 用户服务
type UserService struct {
	userRepo    UserRepository
	wechatRepo  WechatAccountRepository // v1.2 兼容，v1.4 后废弃
	channelRepo UserChannelRepository   // v1.3 新增：多通道支持
}

// NewUserService 创建用户服务
func NewUserService(
	userRepo UserRepository,
	wechatRepo WechatAccountRepository,
	channelRepo UserChannelRepository,
) *UserService {
	return &UserService{
		userRepo:    userRepo,
		wechatRepo:  wechatRepo,
		channelRepo: channelRepo,
	}
}

// GetOrCreateByChannel 根据通道类型和通道用户 ID 获取或创建用户（v1.3 新增）
// 返回: 用户, 通道关联, 是否新创建, 错误
func (s *UserService) GetOrCreateByChannel(channelType string, channelUserID string) (
	*domainuser.User, *domainuser.UserChannel, bool, error,
) {
	// 如果没有 channelRepo，降级使用旧的微信逻辑
	if s.channelRepo == nil {
		if channelType == "wechat" {
			user, created, err := s.GetOrCreateByOpenID(channelUserID)
			return user, nil, created, err
		}
		return nil, nil, false, errors.New("channel repository not initialized")
	}

	// 1. 先查新表 UserChannel
	channel, err := s.channelRepo.GetByChannel(channelType, channelUserID)
	if err == nil && channel != nil {
		// 找到通道关联，获取对应用户
		u, err := s.userRepo.GetByID(channel.UserID)
		if err != nil {
			return nil, nil, false, err
		}
		return u, channel, false, nil
	}

	// 2. 如果是微信，降级查旧表（兼容逻辑）
	if channelType == "wechat" && s.wechatRepo != nil {
		account, err := s.wechatRepo.GetByOpenID(channelUserID)
		if err == nil && account != nil {
			// 找到旧表数据，自动迁移到新表
			u, err := s.userRepo.GetByID(account.UserID)
			if err != nil {
				return nil, nil, false, err
			}
			// 写入新表
			newChannel := domainuser.NewUserChannel(u.ID, channelType, channelUserID)
			if account.UnionID != nil {
				newChannel.SetUnionID(*account.UnionID)
			}
			newChannel.SetAppID(account.AppID)
			err = s.channelRepo.Create(newChannel)
			if err != nil {
				// 写入失败也没关系，下次还会尝试
				return u, nil, false, nil
			}
			return u, newChannel, false, nil
		}
	}

	// 3. 都不存在，创建新用户和通道关联
	u := domainuser.NewUser()
	if err := s.userRepo.Create(u); err != nil {
		return nil, nil, false, err
	}

	// 创建通道关联
	newChannel := domainuser.NewUserChannel(u.ID, channelType, channelUserID)
	if err := s.channelRepo.Create(newChannel); err != nil {
		return nil, nil, false, err
	}

	// 如果是微信，同时写旧表（双写，保证回滚安全）
	if channelType == "wechat" && s.wechatRepo != nil {
		account := domainuser.NewWechatAccount(u.ID, channelUserID)
		_ = s.wechatRepo.Create(account) // 旧表写入失败不影响主流程
	}

	return u, newChannel, true, nil
}

// GetOrCreateByOpenID 根据 OpenID 获取或创建用户（v1.2 兼容接口）
// 内部调用 GetOrCreateByChannel，保证行为完全一致
// 返回: 用户, 是否新创建, 错误
func (s *UserService) GetOrCreateByOpenID(openID string) (*domainuser.User, bool, error) {
	user, _, created, err := s.GetOrCreateByChannel("wechat", openID)
	return user, created, err
}
