package memory

import (
	"context"
	"testing"
	"time"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// journal service 测试(21 号 J2):时间轴拼装/游标分页/档案视图/纠正口。

type fakeBriefSource struct {
	msgs map[int64]*conversation.Message
}

func (f *fakeBriefSource) GetByIDs(ids []int64) ([]*conversation.Message, error) {
	out := make([]*conversation.Message, 0, len(ids))
	for _, id := range ids {
		if m, ok := f.msgs[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func journalSvcSetup(t *testing.T) (*memoryService, memoryrepo.MatterRepository, *gorm.DB, *fakeBriefSource) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}, &memorydomain.MemoryMessageLink{}, &memorydomain.Matter{}, &conversation.Message{}))
	repo := memoryrepo.NewMemoryRepository(db)
	matterRepo := memoryrepo.NewMatterRepository(db)
	briefs := &fakeBriefSource{msgs: map[int64]*conversation.Message{}}
	svc := NewMemoryService(repo, matterRepo, nil, briefs)
	return svc.(*memoryService), matterRepo, db, briefs
}

func seedJournalRow(t *testing.T, db *gorm.DB, m *memorydomain.Memory) *memorydomain.Memory {
	t.Helper()
	require.NoError(t, db.Create(m).Error)
	return m
}

// T1 时间轴:排序、溯源拼装(片段 80 字截断)、无来源消息静默缺失。
func TestJournal_Assembly(t *testing.T) {
	svc, _, db, briefs := journalSvcSetup(t)

	ha := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m1 := seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "10/1 爬了长城", Kind: "fact", HappenedAt: &ha})
	m2 := seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "未识别时间的经历", Kind: "loop"})
	seedJournalRow(t, db, &memorydomain.Memory{UserID: 99, Content: "他人的", Kind: "fact", HappenedAt: &ha})

	long := make([]byte, 120)
	for i := range long {
		long[i] = 'x'
	}
	briefs.msgs[7] = &conversation.Message{ID: 7, Content: string(long), CreatedAt: time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)}
	briefs.msgs[9] = &conversation.Message{ID: 9, Content: "短消息", CreatedAt: time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)}
	require.NoError(t, db.Create(&memorydomain.MemoryMessageLink{MemoryID: m1.ID, MessageID: 7, CreatedAt: time.Now()}).Error)
	require.NoError(t, db.Create(&memorydomain.MemoryMessageLink{MemoryID: m1.ID, MessageID: 9, CreatedAt: time.Now()}).Error)
	// m2 指向不存在的消息 777:静默缺失
	require.NoError(t, db.Create(&memorydomain.MemoryMessageLink{MemoryID: m2.ID, MessageID: 777, CreatedAt: time.Now()}).Error)

	items, next, err := svc.Journal(context.Background(), 42, "all", nil, nil, "", 50)
	require.NoError(t, err)
	assert.Empty(t, next)
	require.Len(t, items, 2)
	assert.Equal(t, "未识别时间的经历", items[0].Content, "有效时间降序:NULL 回落 created_at(现在) 最新")
	first := items[1]
	require.Equal(t, "10/1 爬了长城", first.Content)
	require.NotNil(t, first.HappenedAt)
	require.Len(t, first.Links, 2)
	require.Equal(t, int64(7), first.Links[0].MessageID, "links 按消息时间升序")
	assert.Equal(t, "短消息", first.Links[1].Snippet)
	assert.Equal(t, 80, len([]rune(first.Links[0].Snippet)), "长片段截断 80 字")
	assert.Empty(t, items[0].Links, "指向已消失消息的溯源静默缺失")
}

// T2 游标分页:翻页接力、不重不漏。
func TestJournal_CursorPaging(t *testing.T) {
	svc, _, db, _ := journalSvcSetup(t)
	base := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		ha := base.Add(time.Duration(i) * 24 * time.Hour)
		seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "条目", Kind: "fact", HappenedAt: &ha})
	}
	seen := map[int64]bool{}
	cursor := ""
	pages := 0
	for {
		items, next, err := svc.Journal(context.Background(), 42, "all", nil, nil, cursor, 2)
		require.NoError(t, err)
		for _, it := range items {
			require.False(t, seen[it.ID], "不重复")
			seen[it.ID] = true
		}
		pages++
		if next == "" {
			break
		}
		require.Less(t, pages, 10, "防死循环")
		cursor = next
	}
	assert.Equal(t, 5, len(seen))
	assert.Equal(t, 3, pages)
}

// T3 参数校验:kind / 游标 / 状态。
func TestJournal_Validation(t *testing.T) {
	svc, _, _, _ := journalSvcSetup(t)
	ctx := context.Background()

	_, _, err := svc.Journal(ctx, 42, "episode", nil, nil, "", 10)
	assert.ErrorIs(t, err, ErrJournalInvalidKind)

	_, _, err = svc.Journal(ctx, 42, "all", nil, nil, "not-base64!!", 10)
	assert.ErrorIs(t, err, ErrJournalInvalidCursor)

	_, err = svc.Matters(42, "weird")
	assert.ErrorIs(t, err, ErrJournalInvalidStatus)

	ok, err := svc.UpdateMatter(42, 1, nil, nil)
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrMatterNoFields)

	ok, err = svc.UpdateMatter(42, 1, nil, ptrStr("weird"))
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrJournalInvalidStatus)
}

func ptrStr(s string) *string { return &s }

// T4 档案:列表带计数、详情时间线(pinned 前置)、用户隔离。
func TestMatters_ListAndDetail(t *testing.T) {
	svc, matterRepo, db, _ := journalSvcSetup(t)

	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{UserID: 42, Title: "旅行", StateDesc: "筹备中", Status: memorydomain.MatterStatusActive}))
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{UserID: 42, Title: "装修", StateDesc: "收尾", Status: memorydomain.MatterStatusDone}))
	mm, err := matterRepo.GetByTitle(42, "旅行")
	require.NoError(t, err)

	ha1 := time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	ha2 := time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)
	seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "晚", Kind: "fact", MatterID: &mm.ID, HappenedAt: &ha2})
	seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "早", Kind: "fact", MatterID: &mm.ID, HappenedAt: &ha1})
	seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "置顶", Kind: "fact", MatterID: &mm.ID, HappenedAt: &ha1, Pinned: true})
	seedJournalRow(t, db, &memorydomain.Memory{UserID: 42, Content: "无关", Kind: "fact"})

	list, err := svc.Matters(42, "")
	require.NoError(t, err)
	require.Len(t, list, 2)
	byTitle := map[string]MatterSummary{}
	for _, s := range list {
		byTitle[s.Title] = s
	}
	assert.Equal(t, int64(3), byTitle["旅行"].MemoryCount)
	assert.Equal(t, int64(0), byTitle["装修"].MemoryCount)

	done, err := svc.Matters(42, "done")
	require.NoError(t, err)
	require.Len(t, done, 1)
	assert.Equal(t, "装修", done[0].Title)

	detail, err := svc.MatterDetail(42, mm.ID)
	require.NoError(t, err)
	require.Len(t, detail.Timeline, 3)
	assert.Equal(t, "置顶", detail.Timeline[0].Content, "pinned 前置")
	assert.Equal(t, "早", detail.Timeline[1].Content, "其余有效时间升序")

	// 28号 N3:target_at 随列表/详情回显;普通事项为 nil
	assert.Nil(t, byTitle["旅行"].TargetAt, "未设目标为 nil")
	target := time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local)
	mm.TargetAt = &target
	ok, err := matterRepo.UpdateTarget(42, mm.ID, &target)
	require.NoError(t, err)
	require.True(t, ok)
	list2, err := svc.Matters(42, "")
	require.NoError(t, err)
	var withTarget *MatterSummary
	for i := range list2 {
		if list2[i].ID == mm.ID {
			withTarget = &list2[i]
		}
	}
	require.NotNil(t, withTarget)
	require.NotNil(t, withTarget.TargetAt, "已设目标应回显")
	assert.True(t, withTarget.TargetAt.Equal(target))
	detail2, err := svc.MatterDetail(42, mm.ID)
	require.NoError(t, err)
	require.NotNil(t, detail2.TargetAt, "详情应回显目标日")

	_, err = svc.MatterDetail(99, mm.ID)
	assert.ErrorIs(t, err, ErrMatterNotFound, "他人档案按未找到处理")
}

// T5 纠正口:改 state_desc/Status;不命中(他人)返回 ErrMatterNotFound。
func TestUpdateMatter(t *testing.T) {
	svc, matterRepo, _, _ := journalSvcSetup(t)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{UserID: 42, Title: "旅行", StateDesc: "筹备中", Status: memorydomain.MatterStatusActive}))
	mm, err := matterRepo.GetByTitle(42, "旅行")
	require.NoError(t, err)

	ok, err := svc.UpdateMatter(42, mm.ID, ptrStr("机票已订"), ptrStr("done"))
	require.NoError(t, err)
	require.True(t, ok)
	got, err := matterRepo.GetByID(42, mm.ID)
	require.NoError(t, err)
	assert.Equal(t, "机票已订", got.StateDesc)
	assert.Equal(t, "done", got.Status)

	ok, err = svc.UpdateMatter(99, mm.ID, ptrStr("x"), nil)
	assert.False(t, ok)
	assert.ErrorIs(t, err, ErrMatterNotFound)
}
