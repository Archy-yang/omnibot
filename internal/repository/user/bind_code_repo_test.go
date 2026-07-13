package user

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
)

func setupBindCodeTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.BindCode{})
	require.NoError(t, err)
	return db
}

func TestBindCodeRepository_UpsertAndGetByCode(t *testing.T) {
	db := setupBindCodeTestDB(t)
	repo := NewBindCodeRepository(db)

	expires := time.Now().Add(5 * time.Minute)
	code := &domain.BindCode{
		UserID:    1,
		Code:      "123456",
		ExpiresAt: expires,
	}

	err := repo.Upsert(code)
	require.NoError(t, err)
	assert.Greater(t, code.ID, int64(0))

	found, err := repo.GetByCode("123456")
	require.NoError(t, err)
	assert.Equal(t, int64(1), found.UserID)
	assert.Equal(t, "123456", found.Code)
}

func TestBindCodeRepository_UpsertOverwritesOldCode(t *testing.T) {
	// PRD 4.1: 重新生成码,旧码作废。同 user_id upsert 覆盖。
	db := setupBindCodeTestDB(t)
	repo := NewBindCodeRepository(db)

	_ = repo.Upsert(&domain.BindCode{UserID: 1, Code: "111111", ExpiresAt: time.Now().Add(5 * time.Minute)})
	// 重新生成 -> 旧码 111111 应不再可查
	err := repo.Upsert(&domain.BindCode{UserID: 1, Code: "222222", ExpiresAt: time.Now().Add(5 * time.Minute)})
	require.NoError(t, err)

	_, err = repo.GetByCode("111111")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)

	found, err := repo.GetByCode("222222")
	require.NoError(t, err)
	assert.Equal(t, int64(1), found.UserID)
}

func TestBindCodeRepository_GetByCodeNotFound(t *testing.T) {
	db := setupBindCodeTestDB(t)
	repo := NewBindCodeRepository(db)

	_, err := repo.GetByCode("999999")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestBindCodeRepository_GetByUserID(t *testing.T) {
	db := setupBindCodeTestDB(t)
	repo := NewBindCodeRepository(db)

	_ = repo.Upsert(&domain.BindCode{UserID: 42, Code: "654321", ExpiresAt: time.Now().Add(5 * time.Minute)})

	found, err := repo.GetByUserID(42)
	require.NoError(t, err)
	assert.Equal(t, "654321", found.Code)

	_, err = repo.GetByUserID(999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestBindCodeRepository_DeleteByUserID(t *testing.T) {
	db := setupBindCodeTestDB(t)
	repo := NewBindCodeRepository(db)

	_ = repo.Upsert(&domain.BindCode{UserID: 1, Code: "123456", ExpiresAt: time.Now().Add(5 * time.Minute)})

	err := repo.DeleteByUserID(1)
	require.NoError(t, err)

	_, err = repo.GetByCode("123456")
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = repo.GetByUserID(1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}
