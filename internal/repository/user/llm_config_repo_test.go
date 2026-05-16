package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	domain "omnibot/internal/domain/user"
)

func setupLLMTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)
	return db
}

func TestLLMConfigRepository_CreateAndGet(t *testing.T) {
	db := setupLLMTestDB(t)
	repo := NewLLMConfigRepository(db)

	baseURL := "https://custom.api.com/v1"
	model := "gpt-4"
	cfg := &domain.LLMConfig{
		UserID: 1,
		APIKey: "encrypted-sk-123",
		BaseURL: &baseURL,
		Model:   &model,
		Status:  0,
	}

	err := repo.Create(cfg)
	require.NoError(t, err)
	assert.Greater(t, cfg.ID, int64(0))

	found, err := repo.GetByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), found.UserID)
	assert.Equal(t, "encrypted-sk-123", found.APIKey)
	assert.Equal(t, "https://custom.api.com/v1", *found.BaseURL)
	assert.Equal(t, "gpt-4", *found.Model)
}

func TestLLMConfigRepository_GetNotFound(t *testing.T) {
	db := setupLLMTestDB(t)
	repo := NewLLMConfigRepository(db)

	_, err := repo.GetByUserID(999)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLLMConfigRepository_Update(t *testing.T) {
	db := setupLLMTestDB(t)
	repo := NewLLMConfigRepository(db)

	cfg := &domain.LLMConfig{UserID: 1, APIKey: "old-key"}
	_ = repo.Create(cfg)

	newKey := "new-key-123"
	newURL := "https://new.api.com"
	cfg.APIKey = newKey
	cfg.BaseURL = &newURL

	err := repo.Update(cfg)
	require.NoError(t, err)

	updated, _ := repo.GetByUserID(1)
	assert.Equal(t, newKey, updated.APIKey)
	assert.Equal(t, "https://new.api.com", *updated.BaseURL)
}

func TestLLMConfigRepository_Delete(t *testing.T) {
	db := setupLLMTestDB(t)
	repo := NewLLMConfigRepository(db)

	cfg := &domain.LLMConfig{UserID: 1, APIKey: "test-key"}
	_ = repo.Create(cfg)

	err := repo.Delete(1)
	require.NoError(t, err)

	_, err = repo.GetByUserID(1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLLMConfigRepository_UserUnique(t *testing.T) {
	db := setupLLMTestDB(t)
	repo := NewLLMConfigRepository(db)

	cfg1 := &domain.LLMConfig{UserID: 1, APIKey: "key1"}
	err := repo.Create(cfg1)
	require.NoError(t, err)

	cfg2 := &domain.LLMConfig{UserID: 1, APIKey: "key2"}
	err = repo.Create(cfg2)
	assert.Error(t, err) // 唯一约束冲突
}
