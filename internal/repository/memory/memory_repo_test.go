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

func TestMemoryRepository_GetByID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "用户一记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	memories, _ := repo.ListByUserID(1)
	memoryID := memories[0].ID

	t.Run("正确用户可以获取", func(t *testing.T) {
		memory, err := repo.GetByID(memoryID, 1)
		require.NoError(t, err)
		assert.Equal(t, "用户一记忆", memory.Content)
	})

	t.Run("错误用户返回 nil", func(t *testing.T) {
		memory, err := repo.GetByID(memoryID, 2)
		require.NoError(t, err)
		assert.Nil(t, memory)
	})

	t.Run("不存在 ID 返回 nil", func(t *testing.T) {
		memory, err := repo.GetByID(999, 1)
		require.NoError(t, err)
		assert.Nil(t, memory)
	})
}

func TestMemoryRepository_DeleteByID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "记忆一")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "记忆二")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	memories, _ := repo.ListByUserID(1)
	memory1ID := memories[0].ID

	t.Run("正确用户可以删除", func(t *testing.T) {
		deleted, err := repo.DeleteByID(memory1ID, 1)
		require.NoError(t, err)
		assert.True(t, deleted)

		remaining, _ := repo.ListByUserID(1)
		assert.Len(t, remaining, 1)
		assert.Equal(t, "记忆二", remaining[0].Content)
	})

	t.Run("错误用户无法删除", func(t *testing.T) {
		deleted, err := repo.DeleteByID(999, 2)
		require.NoError(t, err)
		assert.False(t, deleted)
	})

	t.Run("删除不存在 ID 返回 false", func(t *testing.T) {
		deleted, err := repo.DeleteByID(999, 1)
		require.NoError(t, err)
		assert.False(t, deleted)
	})
}

func TestMemoryRepository_UpdateContentByID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "旧内容")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二内容")))

	memories, _ := repo.ListByUserID(1)
	memoryID := memories[0].ID

	t.Run("正确用户可以更新", func(t *testing.T) {
		updated, err := repo.UpdateContentByID(memoryID, 1, "新内容")
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, "新内容", updated.Content)
		assert.Equal(t, int64(1), updated.UserID)
	})

	t.Run("错误用户无法更新", func(t *testing.T) {
		updated, err := repo.UpdateContentByID(memoryID, 2, "恶意修改")
		require.NoError(t, err)
		assert.Nil(t, updated)
	})

	t.Run("不存在 ID 返回 nil", func(t *testing.T) {
		updated, err := repo.UpdateContentByID(999, 1, "新内容")
		require.NoError(t, err)
		assert.Nil(t, updated)
	})
}

// TestDeleteByUserIDAndSource 按 source 清空(注入分层:两个 tab 各清各的)。
func TestDeleteByUserIDAndSource(t *testing.T) {
	db := newRepoTestDB(t)
	repo := NewMemoryRepository(db)
	db.Create(memorydomain.NewMemory(42, "手动A"))
	db.Create(memorydomain.NewAutoMemory(42, "自动B", nil))
	db.Create(memorydomain.NewMemory(43, "别人的"))

	if err := repo.DeleteByUserIDAndSource(42, memorydomain.MemorySourceManual); err != nil {
		t.Fatalf("delete manual: %v", err)
	}
	var got []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&got)
	if len(got) != 1 || got[0].Content != "自动B" {
		t.Errorf("应只剩自动记忆, got %+v", got)
	}
	// 别人的不受影响
	var others int64
	db.Model(&memorydomain.Memory{}).Where("user_id = ?", 43).Count(&others)
	if others != 1 {
		t.Errorf("其他用户不受影响, got %d", others)
	}
}

func newRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.Memory{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
