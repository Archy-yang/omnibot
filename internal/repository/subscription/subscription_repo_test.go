package subscription

import (
	"testing"

	subscriptiondomain "omnibot/internal/domain/subscription"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 订阅仓储测试(14-订阅源管理技术方案 §8 测试清单#6/#8):
// CRUD、同用户同 feed 唯一索引防重订、用户隔离。

func newSubTestDB(t *testing.T) SubscriptionRepository {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&subscriptiondomain.Subscription{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	return NewSubscriptionRepository(db)
}

func TestSubscriptionRepository_Create_And_Get(t *testing.T) {
	repo := newSubTestDB(t)

	sub := &subscriptiondomain.Subscription{
		UserID:     42,
		SiteURL:    "https://example.com",
		FeedURL:    "https://example.com/feed",
		Title:      "示例博客",
		TopicDesc:  "测试用源",
		Status:     subscriptiondomain.StatusActive,
		CreatedVia: subscriptiondomain.ViaManual,
	}
	if err := repo.Create(sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	if sub.ID == 0 {
		t.Fatal("expected ID populated after create")
	}

	got, err := repo.GetByID(42, sub.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.FeedURL != "https://example.com/feed" || got.Title != "示例博客" {
		t.Fatalf("unexpected record: %+v", got)
	}

	// 用户隔离:别的用户按 id 取不到
	if _, err := repo.GetByID(43, sub.ID); err == nil {
		t.Fatal("expected not-found for other user")
	}
}

func TestSubscriptionRepository_UniqueIndex_PreventsDuplicate(t *testing.T) {
	repo := newSubTestDB(t)

	first := &subscriptiondomain.Subscription{UserID: 42, SiteURL: "https://a.com", FeedURL: "https://a.com/feed", Title: "A"}
	if err := repo.Create(first); err != nil {
		t.Fatalf("first create: %v", err)
	}
	dup := &subscriptiondomain.Subscription{UserID: 42, SiteURL: "https://a.com", FeedURL: "https://a.com/feed", Title: "A"}
	if err := repo.Create(dup); err == nil {
		t.Fatal("expected duplicate (user_id,feed_url) to fail")
	}

	// 不同用户同 feed 不冲突
	other := &subscriptiondomain.Subscription{UserID: 43, SiteURL: "https://a.com", FeedURL: "https://a.com/feed", Title: "A"}
	if err := repo.Create(other); err != nil {
		t.Fatalf("same feed other user should be allowed: %v", err)
	}
}

func TestSubscriptionRepository_List_Filter_Delete(t *testing.T) {
	repo := newSubTestDB(t)

	subs := []*subscriptiondomain.Subscription{
		{UserID: 42, SiteURL: "https://a.com", FeedURL: "https://a.com/feed", Title: "A"},
		{UserID: 42, SiteURL: "https://b.com", FeedURL: "https://b.com/feed", Title: "B"},
		{UserID: 43, SiteURL: "https://c.com", FeedURL: "https://c.com/feed", Title: "C"},
	}
	for _, s := range subs {
		if err := repo.Create(s); err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// 全量按用户
	got, err := repo.ListByUserID(42, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 for user 42, got %d", len(got))
	}

	// 暂停后:排除 paused 的列表不返回它
	if err := repo.UpdateStatus(42, got[0].ID, subscriptiondomain.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	active, err := repo.ListByUserID(42, false)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	if len(active) != 1 || active[0].Title != "B" {
		t.Fatalf("expected only B active, got %+v", active)
	}
	all, err := repo.ListByUserID(42, true)
	if err != nil || len(all) != 2 {
		t.Fatalf("includePaused list expected 2, got %d err=%v", len(all), err)
	}

	// 删除
	if err := repo.Delete(42, active[0].ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	remain, _ := repo.ListByUserID(42, true)
	if len(remain) != 1 {
		t.Fatalf("expected 1 after delete, got %d", len(remain))
	}
}

func TestSubscriptionRepository_GetByUserAndFeed(t *testing.T) {
	repo := newSubTestDB(t)

	if _, err := repo.GetByUserAndFeed(42, "https://x.com/feed"); err == nil {
		t.Fatal("expected not-found before create")
	}

	sub := &subscriptiondomain.Subscription{UserID: 42, SiteURL: "https://x.com", FeedURL: "https://x.com/feed", Title: "X"}
	if err := repo.Create(sub); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByUserAndFeed(42, "https://x.com/feed")
	if err != nil || got.ID != sub.ID {
		t.Fatalf("get by feed: got=%v err=%v", got, err)
	}
}
