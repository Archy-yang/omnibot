// Package backtask 收敛全仓的后台执行纪律(2026-10 架构审计 #2):
// 此前 goroutine+recover 模板散落 8 处、ticker 循环 2 处逐字雷同、
// NotifyTurn "单飞+recover+RunOnce" 三胞胎(memory 包内两份几乎逐行相同)。
// 全部收编为本包三个原语,行为与既有模板逐一对齐。
package backtask

import (
	"context"
	"sync"
	"time"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// RunDetached 启动带 panic 防护的后台 goroutine:panic 记日志不上抛。
// 适用于"进程级后台循环"(调度器、长连接)的启动包装。
func RunDetached(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("backtask: "+name+" panic", zap.Any("recover", r))
			}
		}()
		fn()
	}()
}

// RunTickerLoop 阻塞式 ticker 循环:每 interval 触发一次 tick,ctx 结束即退出。
// 阻塞语义与既有调度器 Start 相同——应在 goroutine(或 RunDetached)中运行。
func RunTickerLoop(ctx context.Context, interval time.Duration, tick func(ctx context.Context)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			tick(ctx)
		}
	}
}

// Gate 按 key 单飞的后台执行器(NotifyTurn 三胞胎的合并形态):
// 同 key 上轮未跑完则本次触发直接跳过(不叠跑、不排队);panic 记日志且释放槽位;
// timeoutMs>0 时为执行 ctx 加超时,0 为无超时 Background。
type Gate struct {
	name     string
	inflight sync.Map
}

// NewGate 创建执行闸门;name 仅用于日志定位。
func NewGate(name string) *Gate {
	return &Gate{name: name}
}

// Go 异步触发一次执行。非阻塞:同 key 执行中直接返回 false(跳过)。
func (g *Gate) Go(key int64, timeout time.Duration, fn func(ctx context.Context)) bool {
	if _, busy := g.inflight.LoadOrStore(key, struct{}{}); busy {
		return false
	}
	go func() {
		defer g.inflight.Delete(key)
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("backtask: "+g.name+" panic",
					zap.Int64("key", key), zap.Any("recover", r))
			}
		}()
		var ctx context.Context = context.Background()
		if timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		fn(ctx)
	}()
	return true
}
