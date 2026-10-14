package memory

import (
	"context"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 22 号 R2:沉淀黑名单——建规则即删存量并回报;拦沉淀/注入/检索三处;Journal 管理视图不过滤。

func blacklistSvcSetup(t *testing.T) (*memoryService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}, &memorydomain.MemoryBlacklist{}))
	repo := memoryrepo.NewMemoryRepository(db)
	svc := NewMemoryService(repo, nil, nil, nil).(*memoryService)
	svc.SetBlacklistRepo(memoryrepo.NewBlacklistRepository(db))
	return svc, db
}

// T1 Add:规则落库 + 存量匹配删除并回报条数;重复规则报 ErrBlacklistDuplicate。
func TestBlacklist_AddDeletesExisting(t *testing.T) {
	svc, db := blacklistSvcSetup(t)

	for _, c := range []string{"我的银行卡号是 6222...", "聊聊理财没问题", "银行卡号安全"} {
		require.NoError(t, db.Create(&memorydomain.Memory{UserID: 42, Content: c, Source: memorydomain.MemorySourceAuto}).Error)
	}

	pattern, deleted, err := svc.AddBlacklist(42, "银行卡号")
	require.NoError(t, err)
	assert.NotZero(t, pattern.ID)
	assert.Equal(t, int64(2), deleted, "两条包含匹配的存量被删,无关条目保留")

	var remain []memorydomain.Memory
	require.NoError(t, db.Where("user_id = ?", 42).Find(&remain).Error)
	require.Len(t, remain, 1)
	assert.Equal(t, "聊聊理财没问题", remain[0].Content)

	// 重复 → ErrBlacklistDuplicate
	_, _, err = svc.AddBlacklist(42, "银行卡号")
	assert.ErrorIs(t, err, memoryrepo.ErrBlacklistDuplicate)

	// 空/超长 → 校验错
	_, _, err = svc.AddBlacklist(42, "   ")
	assert.ErrorIs(t, err, ErrBlacklistInvalidPattern)
}

// T2 Remove:解除规则(存量不恢复)。
func TestBlacklist_Remove(t *testing.T) {
	svc, db := blacklistSvcSetup(t)
	rule, _, err := svc.AddBlacklist(42, "秘密项目X")
	require.NoError(t, err)

	ok, err := svc.RemoveBlacklist(42, rule.ID)
	require.NoError(t, err)
	assert.True(t, ok)

	patterns, err := svc.ListBlacklist(42)
	require.NoError(t, err)
	assert.Empty(t, patterns)

	ok, err = svc.RemoveBlacklist(99, rule.ID)
	require.NoError(t, err)
	assert.False(t, ok, "他人规则按未命中")
	_ = db
}

// T3 注入过滤:GetRecentForContext 与 GetMemoryInjection 均不带黑名单命中条目。
func TestBlacklist_InjectionFiltered(t *testing.T) {
	svc, db := blacklistSvcSetup(t)
	require.NoError(t, db.Create(&memorydomain.Memory{UserID: 42, Content: "提及密码重置", Source: memorydomain.MemorySourceManual}).Error)
	require.NoError(t, db.Create(&memorydomain.Memory{UserID: 42, Content: "普通偏好", Source: memorydomain.MemorySourceManual}).Error)
	_, _, err := svc.AddBlacklist(42, "密码")
	require.NoError(t, err)

	recent, err := svc.GetRecentForContext(context.Background(), 42, 10)
	require.NoError(t, err)
	assert.Equal(t, []string{"普通偏好"}, recent, "注入只留未命中条目")

	inj, err := svc.GetMemoryInjection(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, []string{"普通偏好"}, inj.Manual)
}

// T4 检索过滤:黑名单命中条目不返回。
func TestBlacklist_SearchFiltered(t *testing.T) {
	svc, db := blacklistSvcSetup(t)
	require.NoError(t, db.Create(&memorydomain.Memory{UserID: 42, Content: "秘密项目X的进展", Source: memorydomain.MemorySourceAuto}).Error)
	require.NoError(t, db.Create(&memorydomain.Memory{UserID: 42, Content: "别的进展", Source: memorydomain.MemorySourceAuto}).Error)
	_, _, err := svc.AddBlacklist(42, "秘密项目X")
	require.NoError(t, err)

	hits, err := svc.SearchMemories(context.Background(), 42, "进展", 10, nil)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "别的进展", hits[0].Memory.Content)
}

// T5 管线过滤:applyFacts 候选命中规则静默跳过(不落库,水位照常)。
func TestBlacklist_PipelineSkip(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3) // 阈值 3:不足不触发沉淀
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[
		{"content":"用户提到银行卡号 6222","kind":"fact","source_message_ids":[1]},
		{"content":"用户喜欢安静","kind":"fact","source_message_ids":[2]}
	]}`

	// 预置黑名单规则
	require.NoError(t, db.Exec("INSERT INTO memory_blacklists (user_id, pattern, created_at) VALUES (42, '银行卡号', ?)", time.Now()).Error)

	require.NoError(t, p.RunOnce(context.Background(), 42))

	var mems []memorydomain.Memory
	require.NoError(t, db.Where("user_id = ?", 42).Find(&mems).Error)
	require.Len(t, mems, 1, "命中黑名单的候选静默跳过")
	assert.Equal(t, "用户喜欢安静", mems[0].Content)
}
