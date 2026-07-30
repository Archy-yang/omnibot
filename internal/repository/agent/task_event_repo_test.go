package agent

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/agent"
)

func setupTaskEventTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent), TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.TaskEvent{}))
	return db
}

func TestTaskEventRepository_CreateAndList(t *testing.T) {
	db := setupTaskEventTestDB(t)
	repo := NewTaskEventRepository(db)
	repo.Create(&domain.TaskEvent{TaskID: 1, EventType: domain.EventTaskSubmitted, Sequence: 1, SourceAgent: "main"})
	repo.Create(&domain.TaskEvent{TaskID: 1, EventType: domain.EventTaskRunning, Sequence: 2, SourceAgent: "sub"})
	repo.Create(&domain.TaskEvent{TaskID: 2, EventType: domain.EventTaskSubmitted, Sequence: 1, SourceAgent: "main"})

	list, err := repo.ListByTaskID(1)
	require.NoError(t, err)
	assert.Len(t, list, 2)
	assert.Equal(t, domain.EventTaskSubmitted, list[0].EventType)
	assert.Equal(t, domain.EventTaskRunning, list[1].EventType)
	assert.Equal(t, 1, list[0].Sequence)
	assert.Equal(t, 2, list[1].Sequence)
}

func TestTaskEventRepository_Empty(t *testing.T) {
	db := setupTaskEventTestDB(t)
	repo := NewTaskEventRepository(db)
	list, err := repo.ListByTaskID(999)
	require.NoError(t, err)
	assert.Empty(t, list)
}
