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

	user, _, isNew, err := service.GetOrCreateByChannel("web", "session_123")

	assert.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.Greater(t, user.ID, int64(0))
}

func TestUserService_GetOrCreateByChannel_Existing(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	wechatRepo := repo.NewWechatAccountRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, wechatRepo, channelRepo)

	// 第一次创建
	user1, _, isNew1, _ := service.GetOrCreateByChannel("web", "session_456")
	assert.True(t, isNew1)

	// 第二次获取
	user2, _, isNew2, err := service.GetOrCreateByChannel("web", "session_456")

	assert.NoError(t, err)
	assert.False(t, isNew2)
	assert.Equal(t, user1.ID, user2.ID)
}
