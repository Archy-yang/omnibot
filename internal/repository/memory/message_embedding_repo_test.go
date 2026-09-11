package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 消息向量层仓储测试(M7 §10.4 / 测试清单#1):
// upsert 幂等、按用户列出、空切片保护;嵌入水位独立于 digest 水位。

func newMsgEmbTestDB(t *testing.T) (*gorm.DB, interface{}) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.MessageEmbedding{}, &memorydomain.EmbeddingWatermark{}, &memorydomain.DigestWatermark{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db, nil
}

func TestMessageEmbeddingRepository_UpsertBatch_And_List(t *testing.T) {
	db, _ := newMsgEmbTestDB(t)
	repo := NewMessageEmbeddingRepository(db)

	embs := []*memorydomain.MessageEmbedding{
		{MessageID: 1, UserID: 42, Role: "user", Embedding: []float32{0.1, 0.2}, EmbeddingModel: "m"},
		{MessageID: 2, UserID: 42, Role: "assistant", Embedding: []float32{0.3}, EmbeddingModel: "m"},
	}
	if err := repo.UpsertBatch(embs); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	got, err := repo.ListByUserID(42)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].MessageID != 1 || got[1].Role != "assistant" {
		t.Fatalf("list 结果不符: %+v", got)
	}

	// 空切片不报错(GORM Create 空切片会 panic,需保护)
	if err := repo.UpsertBatch(nil); err != nil {
		t.Errorf("nil 批次应静默: %v", err)
	}

	// 同 message_id 再写 = 覆盖(重嵌入幂等),不产生第二行
	if err := repo.UpsertBatch([]*memorydomain.MessageEmbedding{
		{MessageID: 1, UserID: 42, Role: "user", Embedding: []float32{0.9}, EmbeddingModel: "m2"},
	}); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = repo.ListByUserID(42)
	if len(got) != 2 || got[0].EmbeddingModel != "m2" {
		t.Errorf("同 message_id 应覆盖而非新增: %+v", got)
	}

	// 用户隔离
	other, _ := repo.ListByUserID(43)
	if len(other) != 0 {
		t.Errorf("其他用户应为空, got %d", len(other))
	}
}

func TestEmbeddingWatermarkRepository(t *testing.T) {
	db, _ := newMsgEmbTestDB(t)
	repo := NewEmbeddingWatermarkRepository(db)

	// 空水位:0 = 尚未嵌入过(首轮回填存量)
	wm, err := repo.GetByUserID(42)
	if err != nil || wm.LastEmbeddedMsgID != 0 {
		t.Fatalf("空水位应 LastEmbeddedMsgID=0, got %+v err=%v", wm, err)
	}

	if err := repo.Save(42, 100); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := repo.Save(42, 200); err != nil {
		t.Fatalf("save again: %v", err)
	}
	wm, _ = repo.GetByUserID(42)
	if wm.LastEmbeddedMsgID != 200 {
		t.Errorf("重复 save 应更新同一行, got %d", wm.LastEmbeddedMsgID)
	}

	// 与 digest 水位互不影响(独立表)
	digestRepo := NewWatermarkRepository(db)
	dw, _ := digestRepo.GetByUserID(42)
	if dw.LastDigestMsgID != 0 {
		t.Errorf("嵌入水位写入不应影响 digest 水位, got %d", dw.LastDigestMsgID)
	}
}
