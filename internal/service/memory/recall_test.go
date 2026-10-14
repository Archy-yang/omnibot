package memory

import (
	"context"
	"testing"
	"time"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	chatrepo "omnibot/internal/repository/chat"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 22 号 R1:检索的时间/主题维度——时间锚=有效时间(happened_at ?? created_at,与 Journal 同源);
// 过滤在打分循环内,仓储零改动。

func mustTime(s string) *time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return &t
}

// T1 时间窗过滤:窗外剔除、边界含端点、nil filter 行为同现状。
func TestSearchMemories_TimeFilter(t *testing.T) {
	svc, db := retrievalSetup(t)

	seedWithHappened := func(content string, happened *time.Time) *memorydomain.Memory {
		m := memorydomain.NewMemory(42, content)
		m.HappenedAt = happened
		require.NoError(t, db.Create(m).Error)
		return m
	}
	mSep := seedWithHappened("九月去了山里", mustTime("2026-09-15T00:00:00Z"))
	_ = seedWithHappened("三月去了海边", mustTime("2026-03-10T00:00:00Z"))
	_ = seedWithHappened("现在去了山里", nil) // 有效时间回落 created_at(现在),在窗外

	// 时间窗 [9-01, 9-30]:只有九月条目
	hits, err := svc.SearchMemories(context.Background(), 42, "去了", 10,
		&memorydomain.MemoryFilter{From: mustTime("2026-09-01T00:00:00Z"), To: mustTime("2026-09-30T00:00:00Z")})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, mSep.ID, hits[0].Memory.ID)

	// 边界含端点:窗=事件当日 00:00 应命中
	hits, err = svc.SearchMemories(context.Background(), 42, "去了", 10,
		&memorydomain.MemoryFilter{From: mustTime("2026-09-15T00:00:00Z"), To: mustTime("2026-09-15T00:00:00Z")})
	require.NoError(t, err)
	require.Len(t, hits, 1)

	// nil filter:行为同现状(全部候选)
	hits, err = svc.SearchMemories(context.Background(), 42, "去了", 10, nil)
	require.NoError(t, err)
	assert.Len(t, hits, 3)
}

// T2 档案过滤:只看某 matter 挂靠的记忆。
func TestSearchMemories_MatterFilter(t *testing.T) {
	svc, db := retrievalSetup(t)

	mID := int64(5)
	for _, m := range []*memorydomain.Memory{
		{UserID: 42, Content: "旅行订了机票", Kind: "fact", MatterID: &mID},
		{UserID: 42, Content: "旅行买了保险", Kind: "fact", MatterID: &mID},
		{UserID: 42, Content: "项目推进顺利", Kind: "fact"},
	} {
		require.NoError(t, db.Create(m).Error)
	}

	hits, err := svc.SearchMemories(context.Background(), 42, "了", 10, &memorydomain.MemoryFilter{MatterID: &mID})
	require.NoError(t, err)
	require.Len(t, hits, 2, "只返回挂靠该档案的记忆")
	for _, h := range hits {
		require.NotNil(t, h.Memory.MatterID)
		assert.Equal(t, mID, *h.Memory.MatterID)
	}
}

// T3 中期消息检索时间窗:按消息发生时间过滤(复用 M7 测试的种子形态)。
func TestSearchRecentMessages_TimeFilter(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&conversation.Message{}, &memorydomain.MessageEmbedding{}))
	embRepo := memoryrepo.NewMessageEmbeddingRepository(db)
	msgRepo := chatrepo.NewMessageRepository(db)
	svc := NewMemoryService(memoryrepo.NewMemoryRepository(db), nil, embRepo, msgRepo)
	setEmbedding(t, svc, &fakeEmbedding{
		vectors: map[string][]float32{"充电花了多少钱": {1, 0, 0}},
		name:    "fake/m1",
	})

	now := time.Now()
	msgs := []*conversation.Message{
		{ID: 1, UserID: 42, Role: "user", Content: "我充电总共花了多少钱", CreatedAt: now.Add(-40 * 24 * time.Hour)},
		{ID: 2, UserID: 42, Role: "assistant", Content: "老爷，充电总花费给您算好了", CreatedAt: now},
	}
	for _, m := range msgs {
		require.NoError(t, db.Create(m).Error)
	}
	vec := []float32{1, 0, 0}
	require.NoError(t, embRepo.UpsertBatch([]*memorydomain.MessageEmbedding{
		{MessageID: 1, UserID: 42, Role: "user", Embedding: vec, EmbeddingModel: "fake/m1"},
		{MessageID: 2, UserID: 42, Role: "assistant", Embedding: vec, EmbeddingModel: "fake/m1"},
	}))

	// 窗上界 = 10 天前:只有 40 天前的消息
	to := now.Add(-10 * 24 * time.Hour)
	hits, err := svc.SearchRecentMessages(context.Background(), 42, "充电花了多少钱", 5, &memorydomain.MemoryFilter{To: &to})
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, int64(1), hits[0].MessageID)
}
