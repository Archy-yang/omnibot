package memory

import (
	"fmt"
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupMemoryRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}))
	return db
}

func TestMemoryRepository_CreateAndListByUserID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "第一条记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "第二条记忆")))

	memories, err := repo.ListByUserID(1)

	require.NoError(t, err)
	require.Len(t, memories, 2)
	assert.Equal(t, "第一条记忆", memories[0].Content)
	assert.Equal(t, "第二条记忆", memories[1].Content)
}

func TestMemoryRepository_IsolatesUsers(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "用户一记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	memories, err := repo.ListByUserID(1)

	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, int64(1), memories[0].UserID)
	assert.Equal(t, "用户一记忆", memories[0].Content)
}

func TestMemoryRepository_DeleteByUserID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "用户一记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	require.NoError(t, repo.DeleteByUserID(1))

	userOne, err := repo.ListByUserID(1)
	require.NoError(t, err)
	assert.Empty(t, userOne)

	userTwo, err := repo.ListByUserID(2)
	require.NoError(t, err)
	require.Len(t, userTwo, 1)
	assert.Equal(t, "用户二记忆", userTwo[0].Content)
}

func TestMemoryRepository_GetRecentByUserIDReturnsNewestNInAscendingOrder(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	for i := 1; i <= 12; i++ {
		require.NoError(t, repo.Create(memorydomain.NewMemory(1, fmt.Sprintf("记忆 %02d", i))))
	}

	memories, err := repo.GetRecentByUserID(1, 10)

	require.NoError(t, err)
	require.Len(t, memories, 10)
	assert.Equal(t, "记忆 03", memories[0].Content)
	assert.Equal(t, "记忆 12", memories[9].Content)
}
