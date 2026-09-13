package subscription

import (
	"fmt"
	"testing"

	subscriptiondomain "omnibot/internal/domain/subscription"
	subscriptionrepo "omnibot/internal/repository/subscription"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 订阅服务测试(14-订阅源管理技术方案 §8 测试清单#7/#8 + Add 语义):
// 直接 feed 捷径、单候选入库、多候选让用户挑、幂等去重、发现失败如实报错。

// fakeDiscoverer 确定性发现桩:Validate 只认 feedURL 集合,Discover 返回预置候选。
type fakeDiscoverer struct {
	validateOK map[string]*subscriptiondomain.FeedInfo
	discover   map[string][]*subscriptiondomain.FeedInfo
}

func (f *fakeDiscoverer) Discover(siteURL string) ([]*subscriptiondomain.FeedInfo, error) {
	if feeds, ok := f.discover[siteURL]; ok {
		if len(feeds) == 0 {
			return nil, fmt.Errorf("该站点未发现可用的 RSS/Atom 订阅源")
		}
		return feeds, nil
	}
	return nil, fmt.Errorf("该站点未发现可用的 RSS/Atom 订阅源")
}

func (f *fakeDiscoverer) Validate(feedURL string) (*subscriptiondomain.FeedInfo, error) {
	if info, ok := f.validateOK[feedURL]; ok {
		return info, nil
	}
	return nil, fmt.Errorf("不是可用 feed: %s", feedURL)
}

func newSubSvcTestDB(t *testing.T) subscriptionrepo.SubscriptionRepository {
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
	return subscriptionrepo.NewSubscriptionRepository(db)
}

func TestSubscriptionService_Add_DirectFeedURL(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{validateOK: map[string]*subscriptiondomain.FeedInfo{
		"https://blog.test/atom.xml": {FeedURL: "https://blog.test/atom.xml", Title: "博客", Description: "技术与生活"},
	}}
	svc := NewSubscriptionService(repo, disc)

	res, err := svc.Add(42, "https://blog.test/atom.xml", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.Subscription == nil || res.Subscription.FeedURL != "https://blog.test/atom.xml" {
		t.Fatalf("expected direct subscription, got %+v", res)
	}
	// 主题描述缺省由 feed 元数据组合
	if res.Subscription.TopicDesc != "博客：技术与生活" {
		t.Fatalf("expected composed topic desc, got %q", res.Subscription.TopicDesc)
	}
	// SiteURL 记录用户原始输入
	if res.Subscription.SiteURL != "https://blog.test/atom.xml" {
		t.Fatalf("site url should keep user input, got %q", res.Subscription.SiteURL)
	}
}

func TestSubscriptionService_Add_SiteWithSingleFeed(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{discover: map[string][]*subscriptiondomain.FeedInfo{
		"https://site.test": {{FeedURL: "https://site.test/feed", Title: "站点", Description: "d"}},
	}}
	svc := NewSubscriptionService(repo, disc)

	res, err := svc.Add(42, "https://site.test", "")
	if err != nil || res.Subscription == nil {
		t.Fatalf("expected subscribed, got res=%+v err=%v", res, err)
	}
	if res.Subscription.FeedURL != "https://site.test/feed" {
		t.Fatalf("unexpected feed url: %s", res.Subscription.FeedURL)
	}
	if res.Subscription.SiteURL != "https://site.test" {
		t.Fatalf("site url should be the site, got %q", res.Subscription.SiteURL)
	}
}

func TestSubscriptionService_Add_MultipleCandidates(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{discover: map[string][]*subscriptiondomain.FeedInfo{
		"https://multi.test": {
			{FeedURL: "https://multi.test/feed", Title: "主源", Description: ""},
			{FeedURL: "https://multi.test/comments", Title: "评论", Description: ""},
		},
	}}
	svc := NewSubscriptionService(repo, disc)

	res, err := svc.Add(42, "https://multi.test", "")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.Subscription != nil || len(res.Candidates) != 2 {
		t.Fatalf("expected candidates-only result, got %+v", res)
	}
	// 宁漏勿错:多候选时不入库
	all, _ := repo.ListByUserID(42, true)
	if len(all) != 0 {
		t.Fatalf("nothing should be inserted on multiple candidates, got %d", len(all))
	}
}

func TestSubscriptionService_Add_IdempotentDuplicate(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{validateOK: map[string]*subscriptiondomain.FeedInfo{
		"https://blog.test/atom.xml": {FeedURL: "https://blog.test/atom.xml", Title: "博客", Description: ""},
	}}
	svc := NewSubscriptionService(repo, disc)

	if _, err := svc.Add(42, "https://blog.test/atom.xml", ""); err != nil {
		t.Fatalf("first add: %v", err)
	}
	res, err := svc.Add(42, "https://blog.test/atom.xml", "")
	if err != nil {
		t.Fatalf("duplicate add should be idempotent, got err=%v", err)
	}
	if res.Subscription == nil {
		t.Fatalf("expected existing subscription returned, got %+v", res)
	}
	all, _ := repo.ListByUserID(42, true)
	if len(all) != 1 {
		t.Fatalf("expected still 1 record, got %d", len(all))
	}
}

func TestSubscriptionService_Add_NoFeed_Error(t *testing.T) {
	repo := newSubSvcTestDB(t)
	svc := NewSubscriptionService(repo, &fakeDiscoverer{})

	if _, err := svc.Add(42, "https://nothing.test", ""); err == nil {
		t.Fatal("expected explicit error when discovery finds nothing")
	}
}

func TestSubscriptionService_UserDescWins(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{validateOK: map[string]*subscriptiondomain.FeedInfo{
		"https://b.test/feed": {FeedURL: "https://b.test/feed", Title: "T", Description: "D"},
	}}
	svc := NewSubscriptionService(repo, disc)

	res, err := svc.Add(42, "https://b.test/feed", "用户给的主题")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if res.Subscription.TopicDesc != "用户给的主题" {
		t.Fatalf("user desc should win, got %q", res.Subscription.TopicDesc)
	}
}

func TestSubscriptionService_ListRemoveStatus(t *testing.T) {
	repo := newSubSvcTestDB(t)
	disc := &fakeDiscoverer{validateOK: map[string]*subscriptiondomain.FeedInfo{
		"https://b.test/feed": {FeedURL: "https://b.test/feed", Title: "T", Description: ""},
	}}
	svc := NewSubscriptionService(repo, disc)

	res, _ := svc.Add(42, "https://b.test/feed", "")
	id := res.Subscription.ID

	if err := svc.SetStatus(42, id, subscriptiondomain.StatusPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	list, _ := svc.List(42)
	if len(list) != 1 || list[0].Status != subscriptiondomain.StatusPaused {
		t.Fatalf("expected paused record listed, got %+v", list)
	}

	if err := svc.Remove(42, id); err != nil {
		t.Fatalf("remove: %v", err)
	}
	list, _ = svc.List(42)
	if len(list) != 0 {
		t.Fatalf("expected empty after remove, got %d", len(list))
	}
}
