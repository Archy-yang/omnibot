package tool

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	tooldomain "omnibot/internal/domain/tool"
)

func setupToolTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&tooldomain.Tool{}))
	return db
}

func TestToolRepo_UpsertBuiltin_InsertAndKeepEnabledOnUpdate(t *testing.T) {
	db := setupToolTestDB(t)
	repo := NewToolRepository(db)

	// 首次 upsert:插入
	def := tooldomain.ToolDef{
		Name:         "calculator",
		DisplayName:  "计算器",
		Description:  "旧描述",
		Capabilities: []string{"basic"},
		Parameters:   map[string]interface{}{"type": "object"},
	}
	require.NoError(t, repo.UpsertBuiltin(def))

	row, err := repo.GetByName("calculator")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.True(t, row.Enabled) // 默认启用

	// 用户停用
	require.NoError(t, repo.SetEnabled("calculator", false))

	// 发版后描述变更,再次 seed:更新定义字段,不碰 Enabled(用户启停状态优先)
	def.Description = "新描述"
	require.NoError(t, repo.UpsertBuiltin(def))

	row, err = repo.GetByName("calculator")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.Equal(t, "新描述", row.Description)
	assert.False(t, row.Enabled, "seed 不得覆盖用户启停状态")
}

// TestToolRepo_UpsertBuiltin_MainVisibleUpdate main_visible 的 false 必须能通过 seed 落库。
// 回归背景:MainVisible 曾带 gorm "default:true" 标签——GORM 对零值+default 字段在 INSERT 时
// 省略该列,ON CONFLICT 的 excluded.main_visible 取到 DB 默认值 true,导致 RegisterBuiltinSubOnly
// 注册的"子 Agent 专属"(rss_reader/web_read)每次重启 seed 都被写回 true,主 Agent 工具集越界。
func TestToolRepo_UpsertBuiltin_MainVisibleUpdate(t *testing.T) {
	db := setupToolTestDB(t)
	repo := NewToolRepository(db)

	// 首次插入:main_visible=false(子 Agent 专属)
	require.NoError(t, repo.UpsertBuiltin(tooldomain.ToolDef{
		Name: "rss_reader", DisplayName: "RSS", Description: "d", MainVisible: false,
	}))
	row, err := repo.GetByName("rss_reader")
	require.NoError(t, err)
	require.False(t, row.MainVisible, "插入 main_visible=false 应生效")

	// 再次 seed(发版路径):定义变更后 false 仍不被翻回 true
	require.NoError(t, repo.UpsertBuiltin(tooldomain.ToolDef{
		Name: "rss_reader", DisplayName: "RSS", Description: "d2", MainVisible: false,
	}))
	row, err = repo.GetByName("rss_reader")
	require.NoError(t, err)
	assert.False(t, row.MainVisible, "upsert 冲突更新不得把 main_visible=false 翻成 true")
	assert.Equal(t, "d2", row.Description)
}

func TestToolRepo_List(t *testing.T) {
	db := setupToolTestDB(t)
	repo := NewToolRepository(db)

	require.NoError(t, repo.UpsertBuiltin(tooldomain.ToolDef{
		Name: "get_current_time", DisplayName: "时间", Description: "d",
		Capabilities: []string{"basic"}, Parameters: map[string]interface{}{"type": "object"},
	}))

	rows, err := repo.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "get_current_time", rows[0].Name)
}
