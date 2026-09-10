package skill

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	skilldomain "omnibot/internal/domain/skill"
)

func setupSkillTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&skilldomain.Skill{}, &skilldomain.MCPServer{}))
	return db
}

func TestSkillRepo_UpsertBuiltin_InsertAndKeepEnabledOnUpdate(t *testing.T) {
	db := setupSkillTestDB(t)
	repo := NewSkillRepository(db)

	// 首次 upsert:插入
	def := skilldomain.BuiltinDef{
		Name:        "calculator",
		DisplayName: "计算器",
		Description: "旧描述",
		Capabilities: []string{"basic"},
		Parameters: map[string]interface{}{"type": "object"},
	}
	require.NoError(t, repo.UpsertBuiltin(def))

	row, err := repo.GetByName("calculator")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.True(t, row.Enabled) // 默认启用
	assert.Equal(t, skilldomain.SourceBuiltin, row.Source)

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

func TestSkillRepo_List(t *testing.T) {
	db := setupSkillTestDB(t)
	repo := NewSkillRepository(db)

	require.NoError(t, repo.UpsertBuiltin(skilldomain.BuiltinDef{
		Name: "get_current_time", DisplayName: "时间", Description: "d",
		Capabilities: []string{"basic"}, Parameters: map[string]interface{}{"type": "object"},
	}))

	rows, err := repo.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "get_current_time", rows[0].Name)
}
