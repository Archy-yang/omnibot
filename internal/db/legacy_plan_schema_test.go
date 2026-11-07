package db

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	domainagent "omnibot/internal/domain/agent"
)

// v2.12:计划合并进 agent_tasks 后,旧表 task_plans 与旧列 plan_id/plan_step_seq
// 必须被清掉(AutoMigrate 只加不删)。清理必须幂等——每次启动都会跑。

func setupLegacyPlanSchemaDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}))
	// 历史结构:独立计划表 + 两个旧列(手写 DDL 模拟 34 号 v1 的 schema)
	require.NoError(t, db.Exec(`CREATE TABLE task_plans (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		name TEXT,
		goal TEXT,
		status TEXT,
		steps TEXT,
		current_seq INTEGER DEFAULT 0,
		version INTEGER DEFAULT 0,
		created_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE agent_tasks ADD COLUMN plan_id INTEGER`).Error)
	require.NoError(t, db.Exec(`ALTER TABLE agent_tasks ADD COLUMN plan_step_seq INTEGER NOT NULL DEFAULT 0`).Error)
	return db
}

func TestCleanupLegacyPlanSchema(t *testing.T) {
	db := setupLegacyPlanSchemaDB(t)

	// 先确认"旧结构确实在"
	require.True(t, db.Migrator().HasTable("task_plans"))
	require.True(t, db.Migrator().HasColumn(&domainagent.AgentTask{}, "plan_id"))
	require.True(t, db.Migrator().HasColumn(&domainagent.AgentTask{}, "plan_step_seq"))

	require.NoError(t, cleanupLegacyPlanSchema(db))

	assert.False(t, db.Migrator().HasTable("task_plans"), "旧计划表要删掉")
	assert.False(t, db.Migrator().HasColumn(&domainagent.AgentTask{}, "plan_id"), "旧列 plan_id 要删掉")
	assert.False(t, db.Migrator().HasColumn(&domainagent.AgentTask{}, "plan_step_seq"), "旧列 plan_step_seq 要删掉")

	// 幂等:重复调用不报错(每次启动都会跑)
	require.NoError(t, cleanupLegacyPlanSchema(db))
}

// 干净库(新装环境)上跑清理也必须无害。
func TestCleanupLegacyPlanSchema_FreshDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domainagent.AgentTask{}))
	assert.NoError(t, cleanupLegacyPlanSchema(db))
}

// 清理只动结构,不动任务数据。
func TestCleanupLegacyPlanSchema_KeepsTasks(t *testing.T) {
	db := setupLegacyPlanSchemaDB(t)
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("普通任务"), domainagent.SourceWeb, "")
	require.NoError(t, db.Create(task).Error)

	require.NoError(t, cleanupLegacyPlanSchema(db))

	var count int64
	require.NoError(t, db.Model(&domainagent.AgentTask{}).Count(&count).Error)
	assert.Equal(t, int64(1), count, "任务数据不能被清掉")
}
