//go:build postgres_integration

package db

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/domain/user"
)

func TestPostgresIntegration_AutoMigratesAndSupportsPgvector(t *testing.T) {
	db := NewPostgresTestDB(t)

	var extName string
	err := db.Raw("SELECT extname FROM pg_extension WHERE extname = 'vector'").Scan(&extName).Error
	require.NoError(t, err)
	assert.Equal(t, "vector", extName)

	newUser := user.NewUser()
	require.NoError(t, db.Create(newUser).Error)
	assert.Positive(t, newUser.ID)

	config := &user.LLMConfig{
		UserID:   newUser.ID,
		Provider: "openai",
		APIKey:   "encrypted-test-key",
	}
	require.NoError(t, db.Create(config).Error)
	assert.Positive(t, config.ID)

	channel := user.NewUserChannel(newUser.ID, "web", "test-session")
	channel.SetAppID("test-app")
	require.NoError(t, db.Create(channel).Error)
	assert.Positive(t, channel.ID)
}
