package db

// legacy_plan_schema.go — 清理 34 号 v1 的遗留结构(v2.12 合并建模)。
//
// 背景:计划一度用独立表 task_plans + agent_tasks.plan_id/plan_step_seq 承载;
// v2.12 改为 kind=plan 容器 + parent_task_id/step_seq 子任务。AutoMigrate 只加不删,
// 旧表旧列不会自己消失,这里做一次性(幂等)清理。
//
// 只动结构,不动数据行——任务记录本身保留。

import (
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/pkg/logger"
)

// legacyPlanColumns 已废弃的列(合并后被 parent_task_id/step_seq 取代)。
var legacyPlanColumns = []string{"plan_id", "plan_step_seq"}

// cleanupLegacyPlanSchema 删除旧计划表与旧列。幂等:不存在即跳过。
func cleanupLegacyPlanSchema(db *gorm.DB) error {
	dropped := make([]string, 0, 3)

	if db.Migrator().HasTable("task_plans") {
		if err := db.Migrator().DropTable("task_plans"); err != nil {
			return fmt.Errorf("drop legacy task_plans: %w", err)
		}
		dropped = append(dropped, "task_plans")
	}

	for _, col := range legacyPlanColumns {
		if !db.Migrator().HasColumn(&domainagent.AgentTask{}, col) {
			continue
		}
		// 不用 Migrator().DropColumn:sqlite 分支上它返回 nil 但列还在(实测)。
		// 列名来自本文件常量白名单,不存在注入面。
		stmt := "ALTER TABLE agent_tasks DROP COLUMN " + col
		if db.Dialector.Name() == "postgres" {
			stmt = "ALTER TABLE agent_tasks DROP COLUMN IF EXISTS " + col
		}
		if err := db.Exec(stmt).Error; err != nil {
			return fmt.Errorf("drop legacy column %s: %w", col, err)
		}
		if db.Migrator().HasColumn(&domainagent.AgentTask{}, col) {
			return fmt.Errorf("drop legacy column %s: column still exists after drop", col)
		}
		dropped = append(dropped, "agent_tasks."+col)
	}

	if len(dropped) > 0 {
		logger.InfoWithFields("db: legacy plan schema cleaned", zap.Strings("dropped", dropped))
	}
	return nil
}
