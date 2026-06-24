package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
)

func setupServiceTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	assert.NoError(t, err)

	err = db.AutoMigrate(&domain.User{}, &domain.UserChannel{})
	assert.NoError(t, err)

	return db
}

func TestUserService_GetOrCreateByChannel_FirstTime(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, channelRepo)

	user, uc, isNew, err := service.GetOrCreateByChannel("web", "session_123")

	assert.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.NotNil(t, uc)
	assert.Greater(t, user.ID, int64(0))
	assert.Equal(t, "web", uc.ChannelType)
	assert.Equal(t, "session_123", uc.ChannelUserID)
}

func TestUserService_GetOrCreateByChannel_Existing(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, channelRepo)

	// 第一次创建
	user1, _, isNew1, _ := service.GetOrCreateByChannel("web", "session_456")
	assert.True(t, isNew1)

	// 第二次获取
	user2, _, isNew2, err := service.GetOrCreateByChannel("web", "session_456")

	assert.NoError(t, err)
	assert.False(t, isNew2)
	assert.Equal(t, user1.ID, user2.ID)
}

// TestUserService_GetOrCreateByChannel_WechatType v1.8:微信端切到通道路径后,
// 用 channelType="wechat" 走与 web/feishu 同一接口,验证 channel record 落库正确。
func TestUserService_GetOrCreateByChannel_WechatType(t *testing.T) {
	db := setupServiceTestDB(t)
	userRepo := repo.NewUserRepository(db)
	channelRepo := repo.NewUserChannelRepository(db)
	service := NewUserService(userRepo, channelRepo)

	openID := "test_openid_wechat_123"
	user, uc, isNew, err := service.GetOrCreateByChannel("wechat", openID)

	assert.NoError(t, err)
	assert.True(t, isNew)
	assert.NotNil(t, user)
	assert.NotNil(t, uc)
	assert.Equal(t, "wechat", uc.ChannelType)
	assert.Equal(t, openID, uc.ChannelUserID)

	// 二次查询同 openID 应命中
	user2, uc2, isNew2, err := service.GetOrCreateByChannel("wechat", openID)
	assert.NoError(t, err)
	assert.False(t, isNew2)
	assert.Equal(t, user.ID, user2.ID)
	assert.Equal(t, uc.ID, uc2.ID)
}
