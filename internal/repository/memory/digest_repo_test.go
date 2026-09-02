package memory

import (
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDigestTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.ConversationDigest{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return db
}

// TestDigestRepository_CreateAndListActive 创建 + 只列 active(TDD#7)。
func TestDigestRepository_CreateAndListActive(t *testing.T) {
	db := newDigestTestDB(t)
	repo := NewDigestRepository(db)

	if err := repo.Create(memorydomain.NewConversationDigest(42, "纪要A", 1, 20, 20)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := repo.Create(memorydomain.NewConversationDigest(42, "纪要B", 21, 40, 20)); err != nil {
		t.Fatalf("create B: %v", err)
	}

	got, err := repo.ListActiveByUserID(42)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 || got[0].Summary != "纪要A" {
		t.Fatalf("got %d digests, want 2 in id order", len(got))
	}

	// B 标记 superseded 后不再出现
	if err := repo.MarkSuperseded(got[1].ID, 42); err != nil {
		t.Fatalf("mark superseded: %v", err)
	}
	got, err = repo.ListActiveByUserID(42)
	if err != nil {
		t.Fatalf("list after supersede: %v", err)
	}
	if len(got) != 1 || got[0].Summary != "纪要A" {
		t.Errorf("after supersede got %d, want 1 (纪要A)", len(got))
	}
}

// TestDigestRepository_UserIsolation 用户隔离:别人的纪要不可见、不可删(TDD 用户隔离惯例)。
func TestDigestRepository_UserIsolation(t *testing.T) {
	db := newDigestTestDB(t)
	repo := NewDigestRepository(db)

	if err := repo.Create(memorydomain.NewConversationDigest(42, "纪要A", 1, 20, 20)); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.ListActiveByUserID(43)
	if err != nil {
		t.Fatalf("list other user: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("other user should see 0 digests, got %d", len(got))
	}

	deleted, err := repo.DeleteByID(1, 43)
	if err != nil {
		t.Fatalf("delete other user: %v", err)
	}
	if deleted {
		t.Error("delete of another user's digest should return false")
	}
	deleted, err = repo.DeleteByID(1, 42)
	if err != nil || !deleted {
		t.Errorf("owner delete: deleted=%v err=%v, want true/nil", deleted, err)
	}
}
