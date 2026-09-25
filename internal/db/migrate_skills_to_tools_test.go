package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// skills→tools 正名迁移测试(2026-09):builtin 行启停状态保留,旧表消失,幂等可重跑。

// setupLegacySkillsDB 建一个带旧 skills 表的 SQLite 库(手写 DDL,模拟历史 schema)。
func setupLegacySkillsDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE skills (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		display_name TEXT,
		description TEXT,
		source TEXT NOT NULL DEFAULT 'builtin',
		capabilities TEXT,
		params_schema TEXT,
		enabled BOOLEAN NOT NULL,
		main_visible BOOLEAN NOT NULL,
		mcp_server TEXT,
		user_id INTEGER NOT NULL DEFAULT 0,
		tool_name TEXT,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO skills
		(name, display_name, description, source, enabled, main_visible, created_at, updated_at)
		VALUES
		('rss_reader', 'RSS 阅读', '抓取 feed', 'builtin', 0, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		('web_read',   '网页阅读', '读取网页', 'builtin', 1, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP),
		('legacy_mcp', '历史MCP行', '应被忽略', 'mcp',     0, 1, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error)
	return db
}

// TestMigrateSkillsToTools 启停状态保留(source=mcp 行不搬),旧表 drop。
func TestMigrateSkillsToTools(t *testing.T) {
	db := setupLegacySkillsDB(t)
	require.NoError(t, autoMigrate(db)) // 先建 tools 等新表

	require.NoError(t, migrateSkillsToTools(db))

	assert.False(t, db.Migrator().HasTable("skills"), "旧 skills 表应已删除")

	var rssEnabled, webEnabled bool
	require.NoError(t, db.Raw(`SELECT enabled FROM tools WHERE name='rss_reader'`).Scan(&rssEnabled).Error)
	require.NoError(t, db.Raw(`SELECT enabled FROM tools WHERE name='web_read'`).Scan(&webEnabled).Error)
	assert.False(t, rssEnabled, "用户关闭状态必须保留")
	assert.True(t, webEnabled, "用户开启状态必须保留")

	var count int64
	db.Table("tools").Count(&count)
	assert.Equal(t, int64(2), count, "source=mcp 行不迁移")

	// 幂等:再跑一次不报错
	require.NoError(t, migrateSkillsToTools(db))
}

// TestMigrateSkillsToTools_NoLegacyTable 无旧表(全新库)直接跳过。
func TestMigrateSkillsToTools_NoLegacyTable(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, autoMigrate(db))
	require.NoError(t, migrateSkillsToTools(db), "无 skills 表应静默跳过")
}
