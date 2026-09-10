package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// matters 仓储测试(M6,助理人视角):同 title 覆写式更新、活跃清单过滤。
func matterRepoSetup(t *testing.T) (*gorm.DB, MatterRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Matter{}))
	return db, NewMatterRepository(db)
}

func TestUpsertByTitle_CreateThenOverwrite(t *testing.T) {
	db, repo := matterRepoSetup(t)

	// 首次:创建
	require.NoError(t, repo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一西双版纳旅行", StateDesc: "机票别墅已订", Status: memorydomain.MatterStatusActive,
	}))
	var got memorydomain.Matter
	require.NoError(t, db.First(&got, "user_id = ? AND title = ?", 42, "十一西双版纳旅行").Error)
	require.Equal(t, "机票别墅已订", got.StateDesc)

	// 再次:同 title 覆写状态,不新增行(id 不变)
	require.NoError(t, repo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一西双版纳旅行", StateDesc: "交通已定:打车方案", Status: memorydomain.MatterStatusActive,
	}))
	var count int64
	db.Model(&memorydomain.Matter{}).Count(&count)
	require.Equal(t, int64(1), count, "同事项应覆写而非新增")
	require.NoError(t, db.First(&got, "id = ?", got.ID).Error)
	require.Equal(t, "交通已定:打车方案", got.StateDesc)

	// 不同用户同 title 互不影响
	require.NoError(t, repo.UpsertByTitle(&memorydomain.Matter{
		UserID: 43, Title: "十一西双版纳旅行", StateDesc: "别人的旅行", Status: memorydomain.MatterStatusActive,
	}))
	db.Model(&memorydomain.Matter{}).Count(&count)
	require.Equal(t, int64(2), count)
}

func TestListActiveByUserID(t *testing.T) {
	_, repo := matterRepoSetup(t)
	require.NoError(t, repo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "旅行", StateDesc: "推进中", Status: memorydomain.MatterStatusActive,
	}))
	require.NoError(t, repo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "旧项目", StateDesc: "已完结", Status: memorydomain.MatterStatusDone,
	}))

	active, err := repo.ListActiveByUserID(42)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, "旅行", active[0].Title)
}
