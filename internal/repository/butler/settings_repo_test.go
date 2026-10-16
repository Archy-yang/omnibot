package butler

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProactiveNoteRepository_ListRecent(t *testing.T) {
	repo := NewProactiveNoteRepository(setupNoteTestDB(t))
	now := time.Now()
	// 三个用户各自的记录
	for i, uid := range []int64{1, 1, 1, 2} {
		status := "delivered"
		if i == 1 {
			status = "skipped"
		}
		require.NoError(t, repo.Create(newNote(uid, "matter_due", "matter:1", status, now.Add(-time.Duration(i)*time.Hour))))
	}

	// 倒序返回,limit 生效
	list, err := repo.ListRecent(1, 2)
	require.NoError(t, err)
	require.Len(t, list, 2)
	assert.True(t, list[0].CreatedAt.After(list[1].CreatedAt), "新记录在前")
	assert.Equal(t, int64(1), list[0].UserID)

	// 用户隔离
	list, err = repo.ListRecent(2, 10)
	require.NoError(t, err)
	require.Len(t, list, 1)

	// limit<=0 默认 20
	list, err = repo.ListRecent(1, 0)
	require.NoError(t, err)
	assert.Len(t, list, 3)
}
