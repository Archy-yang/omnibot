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

func TestPostgresIntegration_PgvectorExtensionAvailableBeforeVectorMigrations(t *testing.T) {
	db := NewPostgresTestDB(t)

	type vectorMigrationProbe struct {
		ID        int64  `gorm:"primaryKey;autoIncrement"`
		Embedding string `gorm:"type:vector(3);not null"`
	}

	err := db.AutoMigrate(&vectorMigrationProbe{})
	require.NoError(t, err)

	record := vectorMigrationProbe{Embedding: "[1,2,3]"}
	require.NoError(t, db.Create(&record).Error)
	assert.Positive(t, record.ID)
}

func TestPostgresIntegration_EnsurePostgresExtensionsRunsBeforeMigration(t *testing.T) {
	type vectorMigrationProbe struct {
		ID        int64  `gorm:"primaryKey;autoIncrement"`
		Embedding string `gorm:"type:vector(3);not null"`
	}

	testDB := NewPostgresRawTestDB(t)
	err := ensurePostgresExtensions(testDB)
	require.NoError(t, err)

	require.NoError(t, testDB.AutoMigrate(&vectorMigrationProbe{}))
}

func TestPostgresIntegration_EnsurePostgresExtensionsFailsWithoutPgvector(t *testing.T) {
	type vectorMigrationProbeWithoutExtension struct {
		ID        int64  `gorm:"primaryKey;autoIncrement"`
		Embedding string `gorm:"type:vector(3);not null"`
	}

	testDB := NewPostgresRawTestDB(t)
	require.NoError(t, testDB.Exec("DROP EXTENSION IF EXISTS vector").Error)

	err := testDB.AutoMigrate(&vectorMigrationProbeWithoutExtension{})
	require.Error(t, err)
}
