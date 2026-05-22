//go:build postgres_integration

package user

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/db"
	domainuser "omnibot/internal/domain/user"
)

func TestPostgresIntegration_UserRepositories(t *testing.T) {
	testDB := db.NewPostgresTestDB(t)

	userRepo := NewUserRepository(testDB)
	llmRepo := NewLLMConfigRepository(testDB)
	channelRepo := NewUserChannelRepository(testDB)

	newUser := domainuser.NewUser()
	require.NoError(t, userRepo.Create(newUser))
	assert.Positive(t, newUser.ID)

	temperature := 0.75
	maxTokens := 2048
	config := &domainuser.LLMConfig{
		UserID:      newUser.ID,
		Provider:    "openai",
		APIKey:      "encrypted-test-key",
		Temperature: &temperature,
		MaxTokens:   &maxTokens,
	}
	require.NoError(t, llmRepo.Create(config))

	foundConfig, err := llmRepo.GetByUserID(newUser.ID)
	require.NoError(t, err)
	assert.Equal(t, "openai", foundConfig.Provider)
	assert.Equal(t, temperature, *foundConfig.Temperature)
	assert.Equal(t, maxTokens, *foundConfig.MaxTokens)

	duplicateConfig := &domainuser.LLMConfig{
		UserID:   newUser.ID,
		Provider: "openai",
		APIKey:   "another-key",
	}
	assert.Error(t, llmRepo.Create(duplicateConfig))

	channel := domainuser.NewUserChannel(newUser.ID, "web", "session-postgres")
	channel.SetUnionID("union-postgres")
	channel.SetAppID("app-postgres")
	require.NoError(t, channelRepo.Create(channel))

	foundChannel, err := channelRepo.GetByChannel("web", "session-postgres")
	require.NoError(t, err)
	require.NotNil(t, foundChannel.ChannelRawData)
	require.NotNil(t, foundChannel.ChannelRawData.UnionID)
	assert.Equal(t, "union-postgres", *foundChannel.ChannelRawData.UnionID)
	assert.Equal(t, "app-postgres", foundChannel.ChannelRawData.AppID)
}
