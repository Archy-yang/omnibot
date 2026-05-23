package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"omnibot/internal/domain/user"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	assert.NoError(t, err)

	err = db.AutoMigrate(&user.User{})
	assert.NoError(t, err)

	return db
}

func TestUserRepository_Create(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepository(db)

	u := user.NewUser()
	err := repo.Create(u)

	assert.NoError(t, err)
	assert.Greater(t, u.ID, int64(0))
}

func TestUserRepository_GetByID(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepository(db)

	u := user.NewUser()
	_ = repo.Create(u)

	found, err := repo.GetByID(u.ID)

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, u.ID, found.ID)
}

func TestUserRepository_GetByPhone(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepository(db)

	u := user.NewUser()
	u.BindPhone("13800138000")
	_ = repo.Create(u)

	found, err := repo.GetByPhone("13800138000")

	assert.NoError(t, err)
	assert.NotNil(t, found)
	assert.Equal(t, u.ID, found.ID)
}

func TestUserRepository_Update(t *testing.T) {
	db := setupTestDB(t)
	repo := NewUserRepository(db)

	u := user.NewUser()
	_ = repo.Create(u)

	u.BindPhone("13800138000")
	err := repo.Update(u)

	assert.NoError(t, err)

	found, _ := repo.GetByID(u.ID)
	assert.Equal(t, "13800138000", *found.Phone)
	assert.True(t, found.PhoneVerified)
}
