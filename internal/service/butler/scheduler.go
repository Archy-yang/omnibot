package butler

// scheduler.go — 主动管家每日调度(26 号)。
// 30min tick × 进程内"当日已跑"簿记:对位用户每自然日最多跑一次扫描。
// 纪律与 25 号同源:hour 对位与克制闸门都在 ScanService/代码层,调度器只管"何时喊人"。

import (
	"context"
	"sync"
	"time"

	userdomain "omnibot/internal/domain/user"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// EnabledUserLister 开启主动关心的用户清单(user.UserRepository 满足)。
type EnabledUserLister interface {
	ListProactiveEnabled() ([]*userdomain.User, error)
}

// ScanScheduler 主动关心调度器:tick 枚举开启用户,对位且当日未跑才执行。
type ScanScheduler struct {
	scan    *ScanService
	list    EnabledUserLister
	mu      sync.Mutex
	lastRun map[int64]string // userID → "2006-01-02"(进程内防同日重复;重启后由日限闸门兜底)
}

func NewScanScheduler(scan *ScanService, list EnabledUserLister) *ScanScheduler {
	return &ScanScheduler{scan: scan, list: list, lastRun: make(map[int64]string)}
}

// Start 启动调度循环(阻塞,应在 goroutine 中运行;ctx 结束即退出)。
func (s *ScanScheduler) Start(ctx context.Context) {
	// 30min tick:任何配置时刻(h ∈ [8,22))必然被某个 tick 覆盖在 30 分钟内
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *ScanScheduler) tick(ctx context.Context) {
	if _, err := s.RunDue(ctx, time.Now()); err != nil {
		logger.ErrorWithFields("butler: scheduled scan failed", zap.Error(err))
	}
}

// RunDue 枚举开启用户执行一轮"到点判定 + 扫描",返回本轮真正跑了扫描的用户数。
// hour 对位、当日未跑、无错才执行;失败不记"已跑",下个 tick 重试。
func (s *ScanScheduler) RunDue(ctx context.Context, now time.Time) (int, error) {
	users, err := s.list.ListProactiveEnabled()
	if err != nil {
		return 0, err
	}
	today := now.Format("2006-01-02")
	ran := 0
	for _, u := range users {
		s.mu.Lock()
		ranToday := s.lastRun[u.ID] == today
		s.mu.Unlock()
		if ranToday {
			continue
		}
		// 先判定 hour 对位,避免把"未到点"记成"已跑"
		if u.ProactiveHour != now.Hour() {
			continue
		}
		if _, err := s.scan.RunOnce(ctx, u.ID, now); err != nil {
			logger.ErrorWithFields("butler: scan failed",
				zap.Int64("user_id", u.ID), zap.Error(err))
			continue // 失败不记已跑,下个 tick 重试
		}
		s.mu.Lock()
		s.lastRun[u.ID] = today
		s.mu.Unlock()
		ran++
	}
	return ran, nil
}
