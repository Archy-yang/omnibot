package memory

import (
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// journal/matters 仓储测试(21 号 J2):时间轴查询(有效时间=COALESCE(happened_at,created_at))与档案查询。

func journalRepoSetup(t *testing.T) (*gorm.DB, MemoryRepository) {
	t.Helper()
	db := setupMemoryRepoTestDB(t)
	require.NoError(t, db.AutoMigrate(&memorydomain.MemoryMessageLink{}))
	return db, NewMemoryRepository(db)
}

func seedJournalMemories(t *testing.T, db *gorm.DB, userID int64) {
	t.Helper()
	rows := []*memorydomain.Memory{
		{UserID: userID, Content: "有事件时间-较早", Kind: "fact", HappenedAt: ptrTime(time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))},
		{UserID: userID, Content: "有事件时间-较晚", Kind: "fact", HappenedAt: ptrTime(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))},
		{UserID: userID, Content: "无事件时间-新", Kind: "loop"},
		{UserID: 99, Content: "他人记忆", Kind: "fact"},
	}
	for _, m := range rows {
		require.NoError(t, db.Create(m).Error)
	}
	// created_at 手工拉开距离:无事件时间的条目按 created_at 降序可断言
	require.NoError(t, db.Model(&memorydomain.Memory{}).Where("content = ?", "无事件时间-新").
		Update("created_at", time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)).Error)
}

func ptrTime(t time.Time) *time.Time { return &t }

// T1 时间轴排序:有效时间(happened_at ?? created_at)降序;NULL 回落 created_at;用户隔离。
func TestListJournal_OrderAndScope(t *testing.T) {
	db, repo := journalRepoSetup(t)
	seedJournalMemories(t, db, 42)

	rows, err := repo.ListJournal(42, []string{"fact", "loop"}, nil, nil, nil, 0, 50)
	require.NoError(t, err)
	require.Len(t, rows, 3, "他人记忆不入列")
	got := []string{rows[0].Content, rows[1].Content, rows[2].Content}
	assert.Equal(t, []string{"无事件时间-新", "有事件时间-较晚", "有事件时间-较早"}, got,
		"有效时间降序:10-05(NULL 回落) > 10-01 > 09-01")
}

// T2 kind 过滤。
func TestListJournal_KindFilter(t *testing.T) {
	db, repo := journalRepoSetup(t)
	seedJournalMemories(t, db, 42)

	rows, err := repo.ListJournal(42, []string{"loop"}, nil, nil, nil, 0, 50)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "无事件时间-新", rows[0].Content)
}

// T3 时间窗过滤按有效时间;游标谓词取"严格晚于游标"的下一页。
func TestListJournal_WindowAndCursor(t *testing.T) {
	db, repo := journalRepoSetup(t)
	seedJournalMemories(t, db, 42)

	from := time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
	rows, err := repo.ListJournal(42, []string{"fact", "loop"}, &from, nil, nil, 0, 50)
	require.NoError(t, err)
	require.Len(t, rows, 2, "09-01 早于窗口起点,被滤除")

	// 游标 = (10-05, id of 无事件时间-新) → 下一页只剩 10-01 与 09-01
	var curMem memorydomain.Memory
	require.NoError(t, db.Where("content = ?", "无事件时间-新").First(&curMem).Error)
	rows, err = repo.ListJournal(42, []string{"fact", "loop"}, nil, nil, ptrTime(time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)), curMem.ID, 50)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "有事件时间-较晚", rows[0].Content)
}

// T4 溯源批量查询 + 按 matter 计数。
func TestListLinksAndCountByMatter(t *testing.T) {
	_, repo := journalRepoSetup(t)
	m1 := memorydomain.NewMemory(42, "甲")
	require.NoError(t, repo.Create(m1))
	require.NoError(t, repo.CreateLinks(memorydomain.NewMemoryMessageLinks(m1.ID, []int64{7, 9})))

	links, err := repo.ListLinksForMemories([]int64{m1.ID})
	require.NoError(t, err)
	require.Len(t, links, 2)

	empty, err := repo.ListLinksForMemories(nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	counts, err := repo.CountByMatter(42)
	require.NoError(t, err)
	require.Empty(t, counts)

	m2 := memorydomain.NewMemory(42, "乙")
	m2.MatterID = ptrInt64(3)
	require.NoError(t, repo.Create(m2))
	counts, err = repo.CountByMatter(42)
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[3])
}

func ptrInt64(i int64) *int64 { return &i }

// T5 挂靠记忆时间线:pinned 前置,有效时间升序,scope 双重校验(user+matter)。
func TestListByMatter_Timeline(t *testing.T) {
	db, repo := journalRepoSetup(t)
	ha := func(s string) *time.Time {
		tm, _ := time.Parse(time.RFC3339, s)
		return &tm
	}
	for _, m := range []*memorydomain.Memory{
		{UserID: 42, MatterID: ptrInt64(5), Content: "早", Kind: "fact", HappenedAt: ha("2026-09-01T00:00:00Z")},
		{UserID: 42, MatterID: ptrInt64(5), Content: "晚", Kind: "fact", HappenedAt: ha("2026-10-01T00:00:00Z")},
		{UserID: 42, MatterID: ptrInt64(5), Content: "置顶", Kind: "fact", HappenedAt: ha("2026-09-15T00:00:00Z"), Pinned: true},
		{UserID: 42, MatterID: ptrInt64(6), Content: "别家档案", Kind: "fact"},
		{UserID: 99, MatterID: ptrInt64(5), Content: "他人挂靠", Kind: "fact"},
	} {
		require.NoError(t, db.Create(m).Error)
	}

	rows, err := repo.ListByMatter(42, 5)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	got := []string{rows[0].Content, rows[1].Content, rows[2].Content}
	assert.Equal(t, []string{"置顶", "早", "晚"}, got, "pinned 前置,其余有效时间升序")
}
