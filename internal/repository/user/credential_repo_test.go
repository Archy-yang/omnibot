package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
)

func setupCredentialTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.UserCredential{})
	require.NoError(t, err)
	return db
}

func TestCredentialRepository_CreateAndGet(t *testing.T) {
	db := setupCredentialTestDB(t)
	repo := NewCredentialRepository(db)

	cred := &domain.UserCredential{
		UserID:       1,
		PasswordHash: "$2a$10$abcdefghijklmnopqrstuv",
	}

	err := repo.Create(cred)
	require.NoError(t, err)
	assert.Greater(t, cred.ID, int64(0))

	found, err := repo.GetByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), found.UserID)
	assert.Equal(t, "$2a$10$abcdefghijklmnopqrstuv", found.PasswordHash)
}

func TestCredentialRepository_GetNotFound(t *testing.T) {
	db := setupCredentialTestDB(t)
	repo := NewCredentialRepository(db)

	_, err := repo.GetByUserID(999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestCredentialRepository_Update(t *testing.T) {
	db := setupCredentialTestDB(t)
	repo := NewCredentialRepository(db)

	cred := &domain.UserCredential{UserID: 1, PasswordHash: "old-hash"}
	err := repo.Create(cred)
	require.NoError(t, err)

	cred.PasswordHash = "new-hash-xyz"
	err = repo.Update(cred)
	require.NoError(t, err)

	updated, err := repo.GetByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, "new-hash-xyz", updated.PasswordHash)
}

func TestCredentialRepository_UserUnique(t *testing.T) {
	db := setupCredentialTestDB(t)
	repo := NewCredentialRepository(db)

	cred1 := &domain.UserCredential{UserID: 1, PasswordHash: "hash1"}
	err := repo.Create(cred1)
	require.NoError(t, err)

	cred2 := &domain.UserCredential{UserID: 1, PasswordHash: "hash2"}
	err = repo.Create(cred2)
	assert.Error(t, err) // 唯一约束冲突
}
