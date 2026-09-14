package tools

import (
	"context"
	"strings"
	"testing"

	subdomain "omnibot/internal/domain/subscription"
	agentpkg "omnibot/internal/service/agent"
)

// manage_subscriptions 工具测试(14-订阅源管理技术方案 §8 测试清单#9):
// 五个 action 的参数解析与输出语义、用户隔离、多候选回执、id 缺失报错。

type fakeSubManager struct {
	addArgs    [3]interface{} // userID, siteURL, topicDesc
	addResult  *subdomain.AddResult
	addErr     error
	listResult map[int64][]*subdomain.Subscription
	removed    [2]int64 // userID, id
	status     [3]interface{}
}

func (f *fakeSubManager) Add(userID int64, siteURL, topicDesc string) (*subdomain.AddResult, error) {
	f.addArgs = [3]interface{}{userID, siteURL, topicDesc}
	return f.addResult, f.addErr
}

func (f *fakeSubManager) List(userID int64) ([]*subdomain.Subscription, error) {
	return f.listResult[userID], nil
}

func (f *fakeSubManager) Remove(userID, id int64) error {
	f.removed = [2]int64{userID, id}
	return nil
}

func (f *fakeSubManager) SetStatus(userID, id int64, status string) error {
	f.status = [3]interface{}{userID, id, status}
	return nil
}

func newSubToolTestCtx(userID int64) context.Context {
	return context.WithValue(context.Background(), agentpkg.UserIDContextKey, userID)
}

func TestManageSubscriptionsTool_Add_SingleFeed(t *testing.T) {
	fake := &fakeSubManager{addResult: &subdomain.AddResult{Subscription: &subdomain.Subscription{
		ID: 7, FeedURL: "https://b.test/feed", Title: "博客", TopicDesc: "博客：技术",
	}}}
	tool := CreateManageSubscriptionsTool(fake)

	out, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{
		"action": "add", "url": "https://b.test", "topic_desc": "AI 动态",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	// 用户隔离与参数透传
	if fake.addArgs[0] != int64(42) || fake.addArgs[1] != "https://b.test" || fake.addArgs[2] != "AI 动态" {
		t.Fatalf("unexpected add args: %+v", fake.addArgs)
	}
	if !containsAll(out, "已订阅", "https://b.test/feed", "id:7") {
		t.Fatalf("receipt missing pieces: %s", out)
	}
}

func TestManageSubscriptionsTool_Add_MultipleCandidates(t *testing.T) {
	fake := &fakeSubManager{addResult: &subdomain.AddResult{Candidates: []*subdomain.FeedInfo{
		{FeedURL: "https://m.test/feed", Title: "主源"},
		{FeedURL: "https://m.test/comments", Title: "评论"},
	}}}
	tool := CreateManageSubscriptionsTool(fake)

	out, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{
		"action": "add", "url": "https://m.test",
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !containsAll(out, "2 个订阅源", "让用户选择", "https://m.test/feed", "https://m.test/comments") {
		t.Fatalf("candidates receipt wrong: %s", out)
	}
}

func TestManageSubscriptionsTool_List_EmptyAndItems(t *testing.T) {
	tool := CreateManageSubscriptionsTool(&fakeSubManager{listResult: map[int64][]*subdomain.Subscription{}})

	out, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "list"})
	if err != nil || !containsAll(out, "没有任何订阅") {
		t.Fatalf("empty list wrong: out=%q err=%v", out, err)
	}

	fake := &fakeSubManager{listResult: map[int64][]*subdomain.Subscription{
		42: {
			{ID: 1, Title: "A", FeedURL: "https://a.test/feed", TopicDesc: "科技", Status: subdomain.StatusActive},
			{ID: 2, Title: "B", FeedURL: "https://b.test/rss", TopicDesc: "设计", Status: subdomain.StatusPaused},
		},
	}}
	tool = CreateManageSubscriptionsTool(fake)

	out, err = tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "list"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// feed 地址必须在输出里:子 Agent 选源后要靠它调 rss_reader,
	// 缺地址会逼模型自行推算 URL(task#18:标题 AIHOT 被脑补成 aihot.com,全部 DNS 失败)
	if !containsAll(out, "共 2 个订阅源", "[#1] A", "https://a.test/feed", "科技", "[#2] B", "https://b.test/rss", "已暂停") {
		t.Fatalf("list output wrong: %s", out)
	}
}

func TestManageSubscriptionsTool_RemovePauseResume(t *testing.T) {
	fake := &fakeSubManager{}
	tool := CreateManageSubscriptionsTool(fake)

	if _, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "remove", "id": float64(3)}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if fake.removed != [2]int64{42, 3} {
		t.Fatalf("unexpected remove args: %+v", fake.removed)
	}

	if _, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "pause", "id": float64(3)}); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if fake.status[2] != subdomain.StatusPaused {
		t.Fatalf("expected paused, got %v", fake.status[2])
	}

	if _, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "resume", "id": float64(3)}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if fake.status[2] != subdomain.StatusActive {
		t.Fatalf("expected active, got %v", fake.status[2])
	}
}

func TestManageSubscriptionsTool_MissingIDAndUnknownAction(t *testing.T) {
	tool := CreateManageSubscriptionsTool(&fakeSubManager{})

	if _, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "remove"}); err == nil {
		t.Fatal("remove without id should fail")
	}
	if _, err := tool.Execute(newSubToolTestCtx(42), map[string]interface{}{"action": "dance"}); err == nil {
		t.Fatal("unknown action should fail")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"action": "list"}); err == nil {
		t.Fatal("missing userID in ctx should fail")
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
