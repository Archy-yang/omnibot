package user

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
)

func setupStateTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.LarkCLIState{}))
	return db
}

func TestLarkCLIStateRepo_GetMissing(t *testing.T) {
	repo := NewLarkCLIStateRepository(setupStateTestDB(t))
	_, err := repo.GetByUserID(1)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestLarkCLIStateRepo_SaveAndVersionCAS(t *testing.T) {
	repo := NewLarkCLIStateRepository(setupStateTestDB(t))

	// 首存:version=1
	require.NoError(t, repo.Save(&domain.LarkCLIState{UserID: 1, StateBlob: []byte("v1")}))
	got, err := repo.GetByUserID(1)
	require.NoError(t, err)
	assert.Equal(t, int64(1), got.Version)
	assert.Equal(t, []byte("v1"), got.StateBlob)

	// CAS 写回:版本匹配 → 成功且自增
	ok, err := repo.SaveCAS(1, 1, []byte("v2"))
	require.NoError(t, err)
	require.True(t, ok)
	got, _ = repo.GetByUserID(1)
	assert.Equal(t, int64(2), got.Version)
	assert.Equal(t, []byte("v2"), got.StateBlob)

	// 版本不匹配(他机已写) → 拒绝
	ok, err = repo.SaveCAS(1, 1, []byte("stale"))
	require.NoError(t, err)
	assert.False(t, ok, "过期版本应被拒绝")
	got, _ = repo.GetByUserID(1)
	assert.Equal(t, int64(2), got.Version, "内容不被覆盖")
}

func TestLarkCLIStateRepo_Replace(t *testing.T) {
	repo := NewLarkCLIStateRepository(setupStateTestDB(t))
	require.NoError(t, repo.Save(&domain.LarkCLIState{UserID: 2, StateBlob: []byte("a")}))
	require.NoError(t, repo.Save(&domain.LarkCLIState{UserID: 2, StateBlob: []byte("b")}))
	got, err := repo.GetByUserID(2)
	require.NoError(t, err)
	assert.Equal(t, []byte("b"), got.StateBlob, "Save 覆盖式(Upsert);version 从 1 重计")
	assert.Equal(t, int64(1), got.Version)
}
