package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	domain "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
)

func setupServiceTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	err = db.AutoMigrate(&domain.User{}, &domain.WechatAccount{}, &domain.UserChannel{})
	assert.NoError(t, err)

	return db
}

func TestUserService_GetOrCreateByOpenID_FirstTime(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	user, isNew, err := service.GetOrCreateByOpenID("new_openid_123")

	assert.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.Greater(t, user.ID, int64(0))
}

func TestUserService_GetOrCreateByOpenID_Existing(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	// 第一次创建
	user1, isNew1, _ := service.GetOrCreateByOpenID("existing_openid")
	assert.True(t, isNew1)

	// 第二次获取
	user2, isNew2, err := service.GetOrCreateByOpenID("existing_openid")

	assert.NoError(t, err)
	assert.False(t, isNew2)
	assert.Equal(t, user1.ID, user2.ID)
}

func TestUserService_GetOrCreateByChannel_FirstTime(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	user, channel, isNew, err := service.GetOrCreateByChannel("feishu", "feishu_user_123")

	assert.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.NotNil(t, channel)
	assert.Equal(t, "feishu", channel.ChannelType)
	assert.Equal(t, "feishu_user_123", channel.ChannelUserID)
}

func TestUserService_GetOrCreateByChannel_Existing(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	// 第一次创建
	user1, _, isNew1, _ := service.GetOrCreateByChannel("feishu", "feishu_user_456")
	assert.True(t, isNew1)

	// 第二次获取
	user2, _, isNew2, err := service.GetOrCreateByChannel("feishu", "feishu_user_456")

	assert.NoError(t, err)
	assert.False(t, isNew2)
	assert.Equal(t, user1.ID, user2.ID)
}

func TestUserService_GetOrCreateByChannel_WechatMigration(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	// 先在旧表里创建数据（模拟 v1.2 的数据）
	user := domain.NewUser()
	userRepo.Create(user)
	wechatAccount := domain.NewWechatAccount(user.ID, "legacy_openid_789")
	wechatAccount.SetUnionID("unionid_789")
	wechatRepo.Create(wechatAccount)

	// 用新接口查，应该自动迁移到新表
	user2, channel, isNew, err := service.GetOrCreateByChannel("wechat", "legacy_openid_789")

	assert.NoError(t, err)
	assert.False(t, isNew)
	assert.Equal(t, user.ID, user2.ID)
	assert.NotNil(t, channel)
	assert.Equal(t, "wechat", channel.ChannelType)
	assert.Equal(t, "legacy_openid_789", channel.ChannelUserID)
}
