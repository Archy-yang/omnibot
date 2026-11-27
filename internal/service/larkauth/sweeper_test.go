package larkauth

// sweeper_test.go — v2.13 后台收尾驱动。
//
// 重点不是"能收尾",而是"收尾不泄漏":单轮有预算、panic/超时后锁要释放、
// 锁被占用时直接跳过而不是排队。
//
// 测试纪律:绝大多数用例直接调 `sweepOne`(同步),既确定又能在 -race 下跑。
// `SweepOnce` 只单独验一条"会分发 pending 行",用 channel 与后台 goroutine 同步,
// 避免测试自己制造 data race(在共享 mock 上并发读写)。

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeNotifier 记录所有推送。notified 用 channel 与后台 goroutine 同步。
type fakeNotifier struct {
	calls    []notifyCall
	err      error
	notified chan struct{}
}

type notifyCall struct {
	userID  int64
	channel string
	target  string
	text    string
}

func newFakeNotifier() *fakeNotifier { return &fakeNotifier{notified: make(chan struct{}, 8)} }

func (n *fakeNotifier) NotifyAuth(_ context.Context, userID int64, channel, target, text string) error {
	n.calls = append(n.calls, notifyCall{userID: userID, channel: channel, target: target, text: text})
	select {
	case n.notified <- struct{}{}:
	default:
	}
	return n.err
}

// seedPending 造一条 pending 授权(等价于用户刚发过 #飞书授权)。
func seedPending(t *testing.T, repo *mockRepo, cli *mockCLI, userID int64) {
	t.Helper()
	initCrypto(t)
	cli.startURL = "https://verify/first"
	cli.startUserCode = "CODE-1"
	cli.startDevice = "dev-1"
	if _, err := NewService(repo, cli).StartAuth(context.Background(), userID, "cli_x", "sec-1"); err != nil {
		t.Fatalf("seed StartAuth: %v", err)
	}
}

// TestSweepOne_AuthorizesAndNotifies 用户已确认 → 收尾成功并推回发起渠道。
func TestSweepOne_AuthorizesAndNotifies(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{statusOpenID: "ou_done", statusName: "杨琦"}
	seedPending(t, repo, cli, 7)
	repo.rows[7].NotifyChannel, repo.rows[7].NotifyTarget = "feishu", "ou_peer"

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, StatusAuthorized, repo.rows[7].Status)
	assert.Empty(t, repo.rows[7].DeviceCode)
	require.Len(t, n.calls, 1)
	assert.Equal(t, "feishu", n.calls[0].channel)
	assert.Equal(t, "ou_peer", n.calls[0].target)
	assert.Contains(t, n.calls[0].text, "已恢复")
	assert.Contains(t, n.calls[0].text, "杨琦")
}

// TestSweepOne_TimeoutStaysPending 用户还没确认 → 保持 pending,不推送(不打扰)。
func TestSweepOne_TimeoutStaysPending(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{completeErr: context.DeadlineExceeded}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, StatusPending, repo.rows[7].Status, "超时≠失败,pending 必须原样保留")
	assert.Empty(t, n.calls, "还没确认就不要发消息骚扰用户")
}

// TestSweepOne_FailedNotifiesAndPointsToCommand 收尾明确报错 → failed + 给出口。
func TestSweepOne_FailedNotifies(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{completeErr: errors.New("device code expired")}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, StatusFailed, repo.rows[7].Status)
	require.Len(t, n.calls, 1)
	assert.Contains(t, n.calls[0].text, "#飞书授权", "失败要给用户一条可执行的出路")
}

// TestSweepOne_ExpiredReissuesOnce 链接过期 → 自动换发一条新链接(限一次)。
func TestSweepOne_ExpiredReissuesOnce(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)
	repo.rows[7].ExpiresAt = time.Now().Add(-time.Minute)
	repo.rows[7].NotifyChannel, repo.rows[7].NotifyTarget = "feishu", "ou_peer"
	cli.startURL, cli.startDevice = "https://verify/second", "dev-2"

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, 2, cli.startCalls, "过期后应自动换发一次")
	assert.Equal(t, StatusPending, repo.rows[7].Status)
	assert.Equal(t, 1, repo.rows[7].ReissueCount)
	assert.Equal(t, "https://verify/second", repo.rows[7].VerifiedURL)
	require.Len(t, n.calls, 1)
	assert.Contains(t, n.calls[0].text, "https://verify/second")
	assert.Contains(t, n.calls[0].text, "过期")
}

// TestSweepOne_ExpiredTwiceStops 换发过仍然过期 → 停手,等用户手动发起(防无限刷链接)。
func TestSweepOne_ExpiredTwiceStops(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)
	repo.rows[7].ExpiresAt = time.Now().Add(-time.Minute)
	repo.rows[7].ReissueCount = maxReissue // 已经换发过一次

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, 1, cli.startCalls, "不得继续自动换发")
	assert.Equal(t, StatusFailed, repo.rows[7].Status)
	assert.Empty(t, repo.rows[7].VerifiedURL)
	require.Len(t, n.calls, 1)
	assert.Contains(t, n.calls[0].text, "#飞书授权")
}

// TestSweepOne_IgnoresNonPending 非 pending 行不进入收尾(不白跑 CLI)。
func TestSweepOne_IgnoresNonPending(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)
	repo.rows[7].Status = StatusAuthorized

	svc := newService(repo, cli)
	svc.sweepOne(context.Background(), 7)
	assert.Empty(t, cli.completed, "非 pending 不应触发 LoginComplete")
}

// blockingCLI LoginComplete 卡到 ctx 到期才返回(模拟 CLI 挂住)。
type blockingCLI struct {
	*mockCLI
}

func (b *blockingCLI) LoginComplete(ctx context.Context, _ int64, _ string, _ string) error {
	<-ctx.Done()
	return ctx.Err()
}

// TestSweepOne_BudgetBoundsAndReleasesLock 单轮必须有上界,且结束后锁必须释放。
//
// 这是本轮设计的硬约束:backtask.Gate 的超时只取消 ctx、不杀 goroutine,
// 所以 sweepOne 内部每个阻塞调用都得吃 ctx,否则该用户从此不再被推进。
func TestSweepOne_BudgetBoundsAndReleasesLock(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)

	svc := NewService(repo, &blockingCLI{mockCLI: cli})
	svc.budget = 150 * time.Millisecond

	start := time.Now()
	svc.sweepOne(context.Background(), 7)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 2*time.Second, "sweepOne 必须在预算附近返回,不能无限等待")
	mu := svc.userLock(7)
	require.True(t, mu.TryLock(), "一轮结束后 per-user 锁必须已释放")
	mu.Unlock()
}

// TestSweepOne_SkipsWhenLockHeld Web 正在收尾时后台直接跳过,不排队。
func TestSweepOne_SkipsWhenLockHeld(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli)
	mu := svc.userLock(7)
	mu.Lock()
	defer mu.Unlock()

	start := time.Now()
	svc.sweepOne(context.Background(), 7)
	assert.Less(t, time.Since(start), 200*time.Millisecond, "拿不到锁应立即返回")
	assert.Empty(t, cli.completed, "拿不到锁不得发起 CLI 调用")
}

// TestSweepOne_NoNotifierStillUpdatesState 未装配 notifier 时只更新状态,不 panic。
func TestSweepOne_NoNotifierStillUpdatesState(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{statusOpenID: "ou_done"}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli) // 不 SetNotifier
	svc.sweepOne(context.Background(), 7)
	assert.Equal(t, StatusAuthorized, repo.rows[7].Status)
}

// TestSweepOne_NotifierErrorDoesNotBreakState 推送失败不能影响授权状态。
func TestSweepOne_NotifierErrorDoesNotBreakState(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{statusOpenID: "ou_done"}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli)
	n := newFakeNotifier()
	n.err = errors.New("channel offline")
	svc.SetNotifier(n)
	svc.sweepOne(context.Background(), 7)

	assert.Equal(t, StatusAuthorized, repo.rows[7].Status, "推送失败不得回滚状态")
}

// TestSweepOnce_DispatchesPendingRows 扫库 → 分发 → 后台收尾。
// 用 notifier 的 channel 与 goroutine 同步,不在共享 mock 上并发读写。
func TestSweepOnce_DispatchesPendingRows(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{statusOpenID: "ou_done"}
	seedPending(t, repo, cli, 7)

	svc := newService(repo, cli)
	n := newFakeNotifier()
	svc.SetNotifier(n)

	svc.SweepOnce(context.Background())

	select {
	case <-n.notified:
	case <-time.After(2 * time.Second):
		t.Fatal("SweepOnce 应把 pending 行分发出去并完成收尾")
	}
	assert.Equal(t, StatusAuthorized, repo.rows[7].Status)
}

// 扫库失败:一轮直接放弃,不影响下一轮(不能 panic,也不能把 ticker 带崩)。
// 这条用例安全的前提是:ListByStatus 失败时不会启动任何 goroutine。
func TestSweepOnce_ListErrorDoesNotPanic(t *testing.T) {
	repo, cli := newMockRepo(), &mockCLI{}
	seedPending(t, repo, cli, 7)
	repo.listErr = errors.New("db down")

	svc := NewService(repo, cli)
	svc.SweepOnce(context.Background())

	assert.Equal(t, StatusPending, repo.rows[7].Status, "扫库失败不应改动状态")
	assert.Empty(t, cli.completed)
}

// TestBuildAuthPending_IncludesCodeAndTTL 文案:链接、用户码、有效期三件套。
func TestBuildAuthPending_IncludesCodeAndTTL(t *testing.T) {
	text := BuildAuthPending("https://verify/x", "CODE-9", 8)
	assert.True(t, strings.Contains(text, "https://verify/x"))
	assert.True(t, strings.Contains(text, "CODE-9"))
	assert.True(t, strings.Contains(text, "8 分钟"))
}
