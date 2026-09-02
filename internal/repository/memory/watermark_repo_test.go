package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 水位线仓储测试(12-记忆系统技术方案 §4.3 / TDD#6)。
func TestWatermarkRepository_GetEmpty(t *testing.T) {
	db, _ := newWMTestDB(t)
	repo := NewWatermarkRepository(db)

	wm, err := repo.GetByUserID(42)
	if err != nil {
		t.Fatalf("get empty: %v", err)
	}
	if wm == nil || wm.UserID != 42 || wm.LastDigestMsgID != 0 {
		t.Errorf("空水位应返回 UserID=42/LastDigestMsgID=0, got %+v", wm)
	}
}

func TestWatermarkRepository_Upsert(t *testing.T) {
	db, _ := newWMTestDB(t)
	repo := NewWatermarkRepository(db)

	// 首次 upsert = 插入
	if err := repo.Upsert(42, 100); err != nil {
		t.Fatalf("upsert create: %v", err)
	}
	wm, _ := repo.GetByUserID(42)
	if wm.LastDigestMsgID != 100 {
		t.Errorf("LastDigestMsgID = %d, want 100", wm.LastDigestMsgID)
	}

	// 再次 upsert = 更新同一行(不产生第二行)
	if err := repo.Upsert(42, 250); err != nil {
		t.Fatalf("upsert update: %v", err)
	}
	wm, _ = repo.GetByUserID(42)
	if wm.LastDigestMsgID != 250 {
		t.Errorf("LastDigestMsgID = %d, want 250", wm.LastDigestMsgID)
	}
	var count int64
	db.Model(&memorydomain.DigestWatermark{}).Where("user_id = ?", 42).Count(&count)
	if count != 1 {
		t.Errorf("watermark rows = %d, want 1 (单行语义)", count)
	}
}

func newWMTestDB(t *testing.T) (*gorm.DB, interface{}) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.DigestWatermark{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db, nil
}
