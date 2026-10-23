package memory

// 29号 N1 对话内建档服务测试:幂等回落是核心断言——同名档案存在时不写库、
// 不覆盖原 StateDesc(UpsertByTitle 的覆写语义严禁泄漏到建档路径)。

import (
	"context"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateMatterByTitle_Create(t *testing.T) {
	s, db := settleSetup(t)
	target := time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local)

	id, created, err := s.CreateMatterByTitle(context.Background(), 42, "十月体检", "准备约机构", &target)
	require.NoError(t, err)
	assert.True(t, created)
	assert.Positive(t, id)

	var got memorydomain.Matter
	require.NoError(t, db.First(&got, id).Error)
	assert.Equal(t, "十月体检", got.Title)
	assert.Equal(t, "准备约机构", got.StateDesc)
	assert.Equal(t, memorydomain.MatterStatusActive, got.Status)
	require.NotNil(t, got.TargetAt, "target_date 顺手挂目标")
	assert.Equal(t, "2026-10-31", got.TargetAt.Format("2006-01-02"))
}

func TestCreateMatterByTitle_IdempotentFallback(t *testing.T) {
	s, db := settleSetup(t)
	existing := &memorydomain.Matter{UserID: 42, Title: "十月体检", StateDesc: "已有状态:机构已选", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(existing).Error)

	// 同名再建档:回落既有 ID,created=false,原档案一字不动
	id, created, err := s.CreateMatterByTitle(context.Background(), 42, "十月体检", "重复提交的新描述", nil)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, existing.ID, id)

	var got memorydomain.Matter
	require.NoError(t, db.First(&got, existing.ID).Error)
	assert.Equal(t, "已有状态:机构已选", got.StateDesc, "幂等回落不得覆盖原 StateDesc")
	assert.Nil(t, got.TargetAt, "幂等回落不得顺手改目标日")

	// 空标题
	_, _, err = s.CreateMatterByTitle(context.Background(), 42, "   ", "", nil)
	require.ErrorIs(t, err, ErrMatterTitleEmpty)
}

func TestCreateMatterByTitle_PerUserScope(t *testing.T) {
	s, _ := settleSetup(t)
	// user 42 已有同名档案;user 43 建档应各自独立(唯一索引是 user_id+title)
	id42, created42, err := s.CreateMatterByTitle(context.Background(), 42, "装修", "", nil)
	require.NoError(t, err)
	require.True(t, created42)

	id43, created43, err := s.CreateMatterByTitle(context.Background(), 43, "装修", "", nil)
	require.NoError(t, err)
	require.True(t, created43)
	assert.NotEqual(t, id42, id43)

	// 同用户二次 → 幂等
	_, createdAgain, err := s.CreateMatterByTitle(context.Background(), 42, "装修", "", nil)
	require.NoError(t, err)
	assert.False(t, createdAgain)
}

func TestCreateMatterByTitle_TargetNil(t *testing.T) {
	s, db := settleSetup(t)
	id, created, err := s.CreateMatterByTitle(context.Background(), 42, "无目标事项", "", nil)
	require.NoError(t, err)
	require.True(t, created)
	var got memorydomain.Matter
	require.NoError(t, db.First(&got, id).Error)
	assert.Nil(t, got.TargetAt)
}
