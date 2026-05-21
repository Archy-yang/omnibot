package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	domainuser "omnibot/internal/domain/user"
)

func setupChannelTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	assert.NoError(t, err)

	// 自动迁移表结构
	err = db.AutoMigrate(&domainuser.User{}, &domainuser.UserChannel{})
	assert.NoError(t, err)

	return db
}

func Skip_TestUserChannelRepository_Create(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 先创建用户
	user := &domainuser.User{
		Status: 1,
	}
	err := db.Create(user).Error
	assert.NoError(t, err)
	assert.Positive(t, user.ID)

	// 创建用户通道关联
	uc := domainuser.NewUserChannel(user.ID, "wechat", "openid_123")
	uc.SetUnionID("unionid_456")
	uc.SetAppID("appid_789")

	err = repo.Create(uc)
	assert.NoError(t, err)
	assert.Positive(t, uc.ID)
}

func Skip_TestUserChannelRepository_GetByID(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 创建用户
	user := &domainuser.User{Status: 1}
	db.Create(user)

	// 创建通道关联
	uc := domainuser.NewUserChannel(user.ID, "wechat", "openid_123")
	repo.Create(uc)

	// 查询
	found, err := repo.GetByID(uc.ID)
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, "wechat", found.ChannelType)
	assert.Equal(t, "openid_123", found.ChannelUserID)
}

func Skip_TestUserChannelRepository_GetByUserID(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 创建用户
	user := &domainuser.User{Status: 1}
	db.Create(user)

	// 创建多个通道关联
	uc1 := domainuser.NewUserChannel(user.ID, "wechat", "openid_1")
	uc2 := domainuser.NewUserChannel(user.ID, "feishu", "feishu_id_2")
	repo.Create(uc1)
	repo.Create(uc2)

	// 查询用户的所有通道
	ucs, err := repo.GetByUserID(user.ID)
	assert.NoError(t, err)
	assert.Len(t, ucs, 2)
}

func Skip_TestUserChannelRepository_GetByChannel(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 创建用户
	user := &domainuser.User{Status: 1}
	db.Create(user)

	// 创建通道关联
	uc := domainuser.NewUserChannel(user.ID, "wechat", "openid_123")
	repo.Create(uc)

	// 按通道查询
	found, err := repo.GetByChannel("wechat", "openid_123")
	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, user.ID, found.UserID)

	// 查询不存在的
	notFound, err := repo.GetByChannel("wechat", "nonexistent")
	assert.NoError(t, err)
	assert.Nil(t, notFound)
}

func Skip_TestUserChannelRepository_Update(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 创建用户和通道
	user := &domainuser.User{Status: 1}
	db.Create(user)
	uc := domainuser.NewUserChannel(user.ID, "wechat", "openid_123")
	repo.Create(uc)

	// 更新
	uc.SetUnionID("new_unionid")
	uc.SetAppID("new_appid")
	err := repo.Update(uc)
	assert.NoError(t, err)

	// 验证
	updated, _ := repo.GetByID(uc.ID)
	assert.Equal(t, "new_unionid", *updated.ChannelRawData.UnionID)
	assert.Equal(t, "new_appid", updated.ChannelRawData.AppID)
}

func Skip_TestUserChannelRepository_Delete(t *testing.T) {
	db := setupChannelTestDB(t)
	repo := NewUserChannelRepository(db)

	// 创建用户和通道
	user := &domainuser.User{Status: 1}
	db.Create(user)
	uc := domainuser.NewUserChannel(user.ID, "wechat", "openid_123")
	repo.Create(uc)

	// 删除
	err := repo.Delete(uc.ID)
	assert.NoError(t, err)

	// 验证已删除
	deleted, err := repo.GetByID(uc.ID)
	assert.Error(t, err) // gorm.ErrRecordNotFound
	assert.Nil(t, deleted)
}
