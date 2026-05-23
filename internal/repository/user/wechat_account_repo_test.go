package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
)

func setupWechatTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	assert.NoError(t, err)

	err = db.AutoMigrate(&domain.User{}, &domain.WechatAccount{})
	assert.NoError(t, err)

	return db
}

func TestWechatAccountRepository_Create(t *testing.T) {
	db := setupWechatTestDB(t)
	repo := NewWechatAccountRepository(db)

	// 先创建用户
	u := domain.NewUser()
	_ = db.Create(u).Error

	account := domain.NewWechatAccount(u.ID, "test_openid_123")
	err := repo.Create(account)

	assert.NoError(t, err)
	assert.Greater(t, account.ID, int64(0))
}

func TestWechatAccountRepository_GetByOpenID(t *testing.T) {
	db := setupWechatTestDB(t)
	repo := NewWechatAccountRepository(db)

	u := domain.NewUser()
	_ = db.Create(u).Error

	account := domain.NewWechatAccount(u.ID, "test_openid_123")
	_ = repo.Create(account)

	found, err := repo.GetByOpenID("test_openid_123")

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, "test_openid_123", found.OpenID)
	assert.Equal(t, u.ID, found.UserID)
}

func TestWechatAccountRepository_GetByUnionID(t *testing.T) {
	db := setupWechatTestDB(t)
	repo := NewWechatAccountRepository(db)

	u := domain.NewUser()
	_ = db.Create(u).Error

	account := domain.NewWechatAccount(u.ID, "test_openid_123")
	unionID := "test_union_456"
	account.SetUnionID(unionID)
	_ = repo.Create(account)

	found, err := repo.GetByUnionID(unionID)

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, unionID, *found.UnionID)
}
