package backtask

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunDetached_CallsAndSwallowsPanic(t *testing.T) {
	var called atomic.Bool
	RunDetached("test:ok", func() { called.Store(true) })
	require.Eventually(t, called.Load, time.Second, 5*time.Millisecond, "应执行 fn")

	RunDetached("test:panic", func() { panic("boom") }) // 不应打崩测试进程
	time.Sleep(50 * time.Millisecond)                    // 给 panic recover 留窗口
}

func TestRunTickerLoop_TicksAndStopsOnCtx(t *testing.T) {
	var ticks atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		RunTickerLoop(ctx, 10*time.Millisecond, func(context.Context) { ticks.Add(1) })
		close(done)
	}()
	require.Eventually(t, func() bool { return ticks.Load() >= 3 }, time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ctx 取消后循环应退出")
	}
}

func TestGate_SingleFlightPerUser(t *testing.T) {
	running := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	gate := NewGate("test")

	// 第一次触发:进入执行并挂住
	gate.Go(1, 0, func(context.Context) {
		runs.Add(1)
		running <- struct{}{}
		<-release
	})
	<-running

	// 执行期间的再触发(同用户/其他用户):同用户被单飞吞掉,其他用户正常
	gate.Go(1, 0, func(context.Context) { runs.Add(1) })
	gate.Go(2, 0, func(context.Context) { runs.Add(1) })
	assert.Eventually(t, func() bool { return runs.Load() == 2 }, time.Second, 5*time.Millisecond,
		"同用户应被单飞,其他用户不受影响")

	close(release)
	// 被吞掉的触发不排队补跑(设计如此);槽位释放与 close(release) 之间有窗口,
	// 重试触发直到落进去为止
	assert.Eventually(t, func() bool {
		gate.Go(1, 0, func(context.Context) { runs.Add(1) })
		return runs.Load() == 3
	}, time.Second, 10*time.Millisecond, "释放后该用户新触发应可再次执行")
}

func TestGate_PanicReleasesSlot(t *testing.T) {
	gate := NewGate("test")
	var ran1, ran2 atomic.Bool
	gate.Go(1, 0, func(context.Context) {
		ran1.Store(true)
		panic("boom")
	})
	require.Eventually(t, ran1.Load, time.Second, 5*time.Millisecond, "第一次执行应到达")
	gate.Go(1, 0, func(context.Context) { ran2.Store(true) }) // panic 后槽位应释放
	assert.Eventually(t, ran2.Load, time.Second, 5*time.Millisecond, "panic 不得永久占用单飞槽位")
}

func TestGate_TimeoutApplied(t *testing.T) {
	gate := NewGate("test")
	// 极短超时:fn 睡眠超过超时 → ctx 应提前取消
	gate.Go(1, 10*time.Millisecond, func(ctx context.Context) {
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Error("ctx 应在超时后取消")
		}
	})
	time.Sleep(100 * time.Millisecond)
}

func TestGate_ZeroTimeout_NoDeadline(t *testing.T) {
	var wg sync.WaitGroup
	gate := NewGate("test")
	wg.Add(1)
	gate.Go(1, 0, func(ctx context.Context) {
		defer wg.Done()
		_, hasDeadline := ctx.Deadline()
		assert.False(t, hasDeadline, "timeout=0 应为无超时的 Background ctx")
	})
	wg.Wait()
}
