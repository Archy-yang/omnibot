package search

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/search"
	"omnibot/internal/pkg/crypto"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Config{}))
	return db
}

// N2 repo 层:落库即密文(安全红线),按 user_id 唯一,不存在返回 nil 而非 error。
func TestConfigRepository_GetMissingReturnsNil(t *testing.T) {
	repo := NewConfigRepository(setupTestDB(t))

	cfg, err := repo.GetByUserID(42)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestConfigRepository_StoresCiphertextNotPlaintext(t *testing.T) {
	crypto.Init("search-repo-test-key")
	db := setupTestDB(t)
	repo := NewConfigRepository(db)

	encrypted, err := crypto.Encrypt("sk-plain-secret-value-123456")
	require.NoError(t, err)
	cfg := &domain.Config{
		UserID:         1,
		Provider:       domain.ProviderDeepSeek,
		BaseURL:        domain.DefaultBaseURL,
		APIKey:         encrypted,
		Enabled:        true,
		DefaultCount:   5,
		TimeoutSeconds: 20,
	}
	require.NoError(t, repo.Create(cfg))

	got, err := repo.GetByUserID(1)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.NotEqual(t, "sk-plain-secret-value-123456", got.APIKey, "库里不能是明文")
	plain, err := crypto.Decrypt(got.APIKey)
	require.NoError(t, err)
	assert.Equal(t, "sk-plain-secret-value-123456", plain)
}

func TestConfigRepository_UpdateAndDelete(t *testing.T) {
	db := setupTestDB(t)
	repo := NewConfigRepository(db)

	cfg := &domain.Config{
		UserID: 1, Provider: domain.ProviderDeepSeek, BaseURL: domain.DefaultBaseURL,
		APIKey: "cipher", Enabled: true, DefaultCount: 5, TimeoutSeconds: 20,
	}
	require.NoError(t, repo.Create(cfg))

	cfg.Enabled = false
	cfg.DefaultCount = 9
	require.NoError(t, repo.Update(cfg))

	got, err := repo.GetByUserID(1)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.False(t, got.Enabled, "关闭状态必须能落库(不能被 DB 默认值冲掉)")
	assert.Equal(t, 9, got.DefaultCount)

	require.NoError(t, repo.Delete(1))
	got, err = repo.GetByUserID(1)
	require.NoError(t, err)
	assert.Nil(t, got)
}
