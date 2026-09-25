package chat

import (
	"os"
	"testing"

	"omnibot/internal/db"
	"omnibot/pkg/config"
)

// TestManualTurnBackfill 存量消息 Turn 回填(运维工具,默认跳过)。
//
// 用途:Phase 1 上线后一次性把存量 messages 补上 conversation_id/turn_id,
// 并把存量 report 的归属写回 agent_tasks.origin_turn_id。幂等,可重复执行。
//
// 运行(对真实数据库执行回填):
//
//	MANUAL_TURN_BACKFILL=1 go test ./internal/service/chat -run TestManualTurnBackfill -v
//
// 注意:须与服务共用同一数据库;回填只做 UPDATE/INSERT 不改时间线,低风险。
func TestManualTurnBackfill(t *testing.T) {
	if os.Getenv("MANUAL_TURN_BACKFILL") == "" {
		t.Skip("设 MANUAL_TURN_BACKFILL=1 以对真实数据库执行存量消息 Turn 回填")
	}

	configPath := os.Getenv("MANUAL_TURN_BACKFILL_CONFIG")
	if configPath == "" {
		configPath = "../../../configs/config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("加载配置(%s): %v", configPath, err)
	}
	database, err := db.InitDB(&cfg.Database)
	if err != nil {
		t.Fatalf("初始化数据库: %v", err)
	}
	defer database.Close()

	stats, err := BackfillConversationTurns(database.GetGormDB())
	if err != nil {
		t.Fatalf("回填失败: %v", err)
	}
	t.Logf("回填完成: conversations=%d turns=%d messages=%d reports=%d tasks=%d",
		stats.Conversations, stats.Turns, stats.Messages, stats.ReportsLinked, stats.TasksLinked)
}
