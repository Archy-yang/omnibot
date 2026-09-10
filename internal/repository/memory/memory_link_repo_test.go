package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// memory_message_links 仓储测试(M5.2 §7.3):批量幂等写入与原位替换。
func linkRepoSetup(t *testing.T) (*gorm.DB, MemoryRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}, &memorydomain.MemoryMessageLink{}))
	return db, NewMemoryRepository(db)
}

func TestCreateLinks_BatchAndIdempotent(t *testing.T) {
	db, repo := linkRepoSetup(t)
	m := &memorydomain.Memory{UserID: 1, Content: "用户在上海工作", Source: memorydomain.MemorySourceAuto}
	require.NoError(t, db.Create(m).Error)

	// 批量写入 + 重复对/零值自动过滤(NewMemoryMessageLinks 去重)
	links := memorydomain.NewMemoryMessageLinks(m.ID, []int64{3, 5, 5, 3, 0, 8})
	require.Len(t, links, 3)
	require.NoError(t, repo.CreateLinks(links))

	// 重复写入幂等(唯一索引 + OnConflict DoNothing)
	require.NoError(t, repo.CreateLinks(memorydomain.NewMemoryMessageLinks(m.ID, []int64{3, 9})))

	var got []memorydomain.MemoryMessageLink
	require.NoError(t, db.Where("memory_id = ?", m.ID).Order("message_id").Find(&got).Error)
	ids := make([]int64, 0, len(got))
	for _, l := range got {
		ids = append(ids, l.MessageID)
	}
	require.Equal(t, []int64{3, 5, 8, 9}, ids)
}

func TestReplaceLinksForMemory(t *testing.T) {
	db, repo := linkRepoSetup(t)
	m := &memorydomain.Memory{UserID: 1, Content: "用户现在住在北京", Source: memorydomain.MemorySourceAuto}
	require.NoError(t, db.Create(m).Error)
	require.NoError(t, repo.CreateLinks(memorydomain.NewMemoryMessageLinks(m.ID, []int64{1, 2})))

	// 原位更新:整体替换为新集合(新事实依据的消息变了)
	require.NoError(t, repo.ReplaceLinksForMemory(m.ID, []int64{2, 7}))
	var got []memorydomain.MemoryMessageLink
	require.NoError(t, db.Where("memory_id = ?", m.ID).Order("message_id").Find(&got).Error)
	require.Len(t, got, 2)
	require.Equal(t, int64(2), got[0].MessageID)
	require.Equal(t, int64(7), got[1].MessageID)

	// 替换为空 = 清空溯源
	require.NoError(t, repo.ReplaceLinksForMemory(m.ID, nil))
	got = nil
	require.NoError(t, db.Where("memory_id = ?", m.ID).Find(&got).Error)
	require.Len(t, got, 0)
}
