package memory

// 28号 N1 收口服务测试:档案完结(级联)/归档/重开 + loop 按关键词收口三态。
// sqlite 全真环境(AutoMigrate + 真仓储),归属隔离一并覆盖。

import (
	"context"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func settleSetup(t *testing.T) (*memoryService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Matter{}, &memorydomain.Memory{}))
	return &memoryService{
		repo:       memoryrepo.NewMemoryRepository(db),
		matterRepo: memoryrepo.NewMatterRepository(db),
	}, db
}

func TestSettleMatter_Close_CascadesLoops(t *testing.T) {
	s, db := settleSetup(t)
	matter := &memorydomain.Matter{UserID: 42, Title: "父亲的诊治", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(matter).Error)
	l1 := &memorydomain.Memory{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "查肝功指标含义", MatterID: &matter.ID}
	l2 := &memorydomain.Memory{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "约复诊", MatterID: &matter.ID}
	orphan := &memorydomain.Memory{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "无关孤儿待办"}
	require.NoError(t, db.Create([]*memorydomain.Memory{l1, l2, orphan}).Error)

	res, err := s.SettleMatter(context.Background(), 42, matter.ID, "close")
	require.NoError(t, err)
	assert.Equal(t, "父亲的诊治", res.Title)
	assert.Equal(t, 2, res.CascadedLoops, "只级联本档案下的 open loops,孤儿待办不动")

	// matter → done;两条 loop → closed;孤儿仍 open
	var got memorydomain.Matter
	require.NoError(t, db.First(&got, matter.ID).Error)
	assert.Equal(t, memorydomain.MatterStatusDone, got.Status)
	var n int64
	require.NoError(t, db.Model(&memorydomain.Memory{}).Where("id IN ?", []int64{l1.ID, l2.ID}).
		Where("loop_status = ?", memorydomain.MemoryLoopStatusClosed).Count(&n).Error)
	assert.Equal(t, int64(2), n)
	var orphanGot memorydomain.Memory
	require.NoError(t, db.First(&orphanGot, orphan.ID).Error)
	assert.Equal(t, memorydomain.MemoryLoopStatusOpen, orphanGot.LoopStatus)
}

func TestSettleMatter_Archive_Reopen(t *testing.T) {
	s, db := settleSetup(t)
	matter := &memorydomain.Matter{UserID: 42, Title: "西双版纳行程", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(matter).Error)

	res, err := s.SettleMatter(context.Background(), 42, matter.ID, "archive")
	require.NoError(t, err)
	assert.Equal(t, 0, res.CascadedLoops, "归档不级联")
	var got memorydomain.Matter
	require.NoError(t, db.First(&got, matter.ID).Error)
	assert.Equal(t, memorydomain.MatterStatusArchived, got.Status)

	_, err = s.SettleMatter(context.Background(), 42, matter.ID, "reopen")
	require.NoError(t, err)
	require.NoError(t, db.First(&got, matter.ID).Error)
	assert.Equal(t, memorydomain.MatterStatusActive, got.Status)
}

func TestSettleMatter_NotFoundAndOwnership(t *testing.T) {
	s, db := settleSetup(t)
	matter := &memorydomain.Matter{UserID: 42, Title: "我的档案", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(matter).Error)

	// 不存在
	_, err := s.SettleMatter(context.Background(), 42, 999, "close")
	require.ErrorIs(t, err, ErrMatterNotFound)
	// 他人档案 = 未命中(归属隔离)
	_, err = s.SettleMatter(context.Background(), 43, matter.ID, "close")
	require.ErrorIs(t, err, ErrMatterNotFound)
	// 非法 action
	_, err = s.SettleMatter(context.Background(), 42, matter.ID, "delete")
	require.Error(t, err)
}

func TestCloseLoopByQuery_ThreeStates(t *testing.T) {
	s, db := settleSetup(t)
	require.NoError(t, db.Create([]*memorydomain.Memory{
		{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "查证中国官方对 AI 冷战的回应"},
		{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "整理 AI 冷战相关资料"},
		{UserID: 42, Kind: memorydomain.MemoryKindLoop, Content: "给父亲约复诊"},
	}).Error)

	// 多命中 → 返回候选,不动作
	cands, closed, err := s.CloseLoopByQuery(context.Background(), 42, "AI 冷战")
	require.NoError(t, err)
	assert.False(t, closed)
	assert.Len(t, cands, 2)

	// 唯一命中 → 关闭
	cands, closed, err = s.CloseLoopByQuery(context.Background(), 42, "复诊")
	require.NoError(t, err)
	assert.True(t, closed)
	assert.Empty(t, cands)
	var openN int64
	require.NoError(t, db.Model(&memorydomain.Memory{}).
		Where("user_id = ? AND kind = ? AND loop_status = ?", 42, memorydomain.MemoryKindLoop, memorydomain.MemoryLoopStatusOpen).
		Count(&openN).Error)
	assert.Equal(t, int64(2), openN, "复诊 loop 应已 closed")

	// 零命中
	cands, closed, err = s.CloseLoopByQuery(context.Background(), 42, "不存在的待办")
	require.NoError(t, err)
	assert.False(t, closed)
	assert.Empty(t, cands)
}
