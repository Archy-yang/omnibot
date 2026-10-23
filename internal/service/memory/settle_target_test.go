package memory

// 28号 N2 目标维度测试:set_target 写路径(设/清/归属隔离)。

import (
	"context"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetTargetMatter_SetAndClear(t *testing.T) {
	s, db := settleSetup(t)
	matter := &memorydomain.Matter{UserID: 42, Title: "完成体检", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(matter).Error)

	// 设目标
	target := time.Date(2026, 10, 31, 0, 0, 0, 0, time.Local)
	title, err := s.SetTargetMatter(context.Background(), 42, matter.ID, &target)
	require.NoError(t, err)
	assert.Equal(t, "完成体检", title)

	var got memorydomain.Matter
	require.NoError(t, db.First(&got, matter.ID).Error)
	require.NotNil(t, got.TargetAt)
	assert.True(t, got.TargetAt.Equal(target))

	// 清目标(nil = 恢复普通事项)
	_, err = s.SetTargetMatter(context.Background(), 42, matter.ID, nil)
	require.NoError(t, err)
	// 注意:不能用复用的 got 再 First——gorm 对 NULL 列不回填已非零的指针字段(M6 同款坑),
	// 必须用新 struct 接
	var cleared memorydomain.Matter
	require.NoError(t, db.First(&cleared, matter.ID).Error)
	assert.Nil(t, cleared.TargetAt, "清除目标应置 NULL")
}

func TestSetTargetMatter_NotFoundAndOwnership(t *testing.T) {
	s, db := settleSetup(t)
	matter := &memorydomain.Matter{UserID: 42, Title: "我的档案", Status: memorydomain.MatterStatusActive}
	require.NoError(t, db.Create(matter).Error)

	target := time.Now()
	_, err := s.SetTargetMatter(context.Background(), 42, 999, &target)
	require.ErrorIs(t, err, ErrMatterNotFound)
	_, err = s.SetTargetMatter(context.Background(), 43, matter.ID, &target)
	require.ErrorIs(t, err, ErrMatterNotFound)
}
