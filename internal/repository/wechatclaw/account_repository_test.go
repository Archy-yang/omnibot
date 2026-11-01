package wechatclaw

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/wechatclaw"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Account{}))
	return db
}

func TestAccountRepository_GetEmpty(t *testing.T) {
	repo := NewAccountRepository(setupTestDB(t))
	account, err := repo.Get()
	require.NoError(t, err)
	assert.Nil(t, account)
}

func TestAccountRepository_SaveAndGet(t *testing.T) {
	repo := NewAccountRepository(setupTestDB(t))

	account := &domain.Account{
		BotID:       "bot-1",
		ILinkUserID: "u-1",
		BaseURL:     "https://ilinkai.weixin.qq.com",
		TokenCipher: "cipher-1",
		Status:      domain.StatusActive,
	}
	require.NoError(t, repo.Save(account))
	assert.Greater(t, account.ID, int64(0))

	got, err := repo.Get()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "bot-1", got.BotID)
	assert.Equal(t, "cipher-1", got.TokenCipher)
	assert.True(t, got.IsActive())
}

func TestAccountRepository_SingleAccountOverwrite(t *testing.T) {
	repo := NewAccountRepository(setupTestDB(t))

	first := &domain.Account{BotID: "bot-1", TokenCipher: "c1", Status: domain.StatusActive}
	require.NoError(t, repo.Save(first))
	first.TokenCipher = "c1-new"
	first.SyncBuf = "buf-1"
	require.NoError(t, repo.Save(first))

	got, err := repo.Get()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "c1-new", got.TokenCipher)
	assert.Equal(t, "buf-1", got.SyncBuf)
}

func TestAccountRepository_UpdateSyncBufAndStatus(t *testing.T) {
	repo := NewAccountRepository(setupTestDB(t))
	account := &domain.Account{BotID: "bot-1", TokenCipher: "c1", Status: domain.StatusActive}
	require.NoError(t, repo.Save(account))

	require.NoError(t, repo.UpdateSyncBuf(account.ID, "buf-9"))
	require.NoError(t, repo.UpdateStatus(account.ID, domain.StatusExpired))

	got, err := repo.Get()
	require.NoError(t, err)
	assert.Equal(t, "buf-9", got.SyncBuf)
	assert.Equal(t, domain.StatusExpired, got.Status)
}

func TestAccountRepository_DeleteAll(t *testing.T) {
	repo := NewAccountRepository(setupTestDB(t))
	require.NoError(t, repo.Save(&domain.Account{BotID: "bot-1", TokenCipher: "c1", Status: domain.StatusActive}))

	require.NoError(t, repo.DeleteAll())

	got, err := repo.Get()
	require.NoError(t, err)
	assert.Nil(t, got)
}
