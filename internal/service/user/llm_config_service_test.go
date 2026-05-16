package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	domain "omnibot/internal/domain/user"
	repo "omnibot/internal/repository/user"
)

func setupServiceDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.LLMConfig{})
	require.NoError(t, err)
	return db
}

func TestLLMConfigService_SetAndGetAPIKey(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	err := service.SetAPIKey(1, "sk-test-api-key-1234567890abcdefghijklmnopqrst")
	require.NoError(t, err)

	apiKey, baseURL, model, hasCustom, err := service.GetConfigForUser(1)
	require.NoError(t, err)
	assert.True(t, hasCustom)
	assert.Equal(t, "sk-test-api-key-1234567890abcdefghijklmnopqrst", apiKey)
	assert.Equal(t, "https://api.openai.com/v1", baseURL)
	assert.Equal(t, "gpt-3.5-turbo", model)
}

func TestLLMConfigService_GetConfigView_Masked(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_ = service.SetAPIKey(1, "sk-abcdefghijklmnopqrstuvwxyz0123456789")

	view, err := service.GetConfigView(1)
	require.NoError(t, err)
	assert.True(t, view.HasConfig)
	assert.Equal(t, "sk-ab...89", view.APIKeyMasked)
}

func TestLLMConfigService_NoConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_, _, _, hasCustom, err := service.GetConfigForUser(999)
	require.NoError(t, err)
	assert.False(t, hasCustom)
}

func TestLLMConfigService_ClearConfig(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	_ = service.SetAPIKey(1, "sk-test-key-1234567890abcdefghijklmnopqrst")
	_, _, _, hasCustom, _ := service.GetConfigForUser(1)
	assert.True(t, hasCustom)

	err := service.ClearConfig(1)
	require.NoError(t, err)

	_, _, _, hasCustom, _ = service.GetConfigForUser(1)
	assert.False(t, hasCustom)
}

func TestLLMConfigService_Validation_APIKeyFormat(t *testing.T) {
	db := setupServiceDB(t)
	llmRepo := repo.NewLLMConfigRepository(db)
	service := NewLLMConfigService(llmRepo)

	// 不以 sk- 开头的 Key 应该被拒绝
	err := service.SetAPIKey(1, "invalid-key")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "API Key 必须以 sk- 开头")
}
