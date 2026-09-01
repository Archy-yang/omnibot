package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 迁移回归测试(12-记忆系统技术方案 §4.1 / TDD#1):
// 老库(memories 表只有 MVP 五列)迁移到新 schema 后:
//  1. 新列全部就位且老数据 Source 回填 manual、Embedding 为 NULL、EmbeddingModel 为空
//  2. 老数据内容无损可读
//  3. 新写入携带新列正常落库
func TestMigration_OldMemoryTableGainsColumns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	// 1) 模拟 MVP 时期的老表结构(无 Source/Embedding 等新列) + 一条老数据
	if err := db.Exec(`CREATE TABLE memories (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id INTEGER NOT NULL,
		content TEXT NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`).Error; err != nil {
		t.Fatalf("create legacy table: %v", err)
	}
	if err := db.Exec(`INSERT INTO memories (user_id, content, created_at, updated_at)
		VALUES (42, '老记忆:用户在上海工作', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`).Error; err != nil {
		t.Fatalf("insert legacy row: %v", err)
	}

	// 2) AutoMigrate 到新 schema
	if err := db.AutoMigrate(&memorydomain.Memory{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	// 3) 老数据可读,新列带默认值
	var legacy memorydomain.Memory
	if err := db.First(&legacy, "user_id = ?", 42).Error; err != nil {
		t.Fatalf("read legacy row: %v", err)
	}
	if legacy.Content != "老记忆:用户在上海工作" {
		t.Errorf("legacy content lost: %q", legacy.Content)
	}
	if legacy.Source != memorydomain.MemorySourceManual {
		t.Errorf("legacy Source = %q, want %q (default:manual 回填)", legacy.Source, memorydomain.MemorySourceManual)
	}
	if legacy.Embedding != nil {
		t.Errorf("legacy Embedding should be NULL, got len=%d", len(legacy.Embedding))
	}
	if legacy.EmbeddingModel != "" {
		t.Errorf("legacy EmbeddingModel = %q, want empty", legacy.EmbeddingModel)
	}
	if legacy.SourceMessageID != nil {
		t.Errorf("legacy SourceMessageID should be NULL, got %v", *legacy.SourceMessageID)
	}

	// 4) 新写入携带新列正常落库
	m := memorydomain.NewAutoMemory(42, "新记忆:自动沉淀", &legacy.ID)
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("create auto memory: %v", err)
	}
	var got memorydomain.Memory
	if err := db.First(&got, "id = ?", m.ID).Error; err != nil {
		t.Fatalf("read new row: %v", err)
	}
	if got.Source != memorydomain.MemorySourceAuto {
		t.Errorf("new row Source = %q, want auto", got.Source)
	}
	if got.SourceMessageID == nil || *got.SourceMessageID != legacy.ID {
		t.Errorf("new row SourceMessageID = %v, want %d", got.SourceMessageID, legacy.ID)
	}
}

// TestMigration_DigestTables 新表(digests/watermarks)由 AutoMigrate 建出,唯一约束生效(技术方案 §4.2/4.3)。
func TestMigration_DigestTables(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.ConversationDigest{}, &memorydomain.DigestWatermark{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}

	// digest: 覆盖区间落库
	d := memorydomain.NewConversationDigest(42, "纪要A", 1, 20, 20)
	if err := db.Create(d).Error; err != nil {
		t.Fatalf("create digest: %v", err)
	}
	// watermark: 单用户单行(主键即 user_id),Upsert 语义不产生第二行
	wm := &memorydomain.DigestWatermark{UserID: 42, LastDigestMsgID: 20}
	if err := db.Create(wm).Error; err != nil {
		t.Fatalf("create watermark: %v", err)
	}
	err = db.Create(&memorydomain.DigestWatermark{UserID: 42, LastDigestMsgID: 30}).Error
	if err == nil {
		t.Error("同用户第二条 watermark 应因主键冲突被拒(单行水位语义)")
	}
}
