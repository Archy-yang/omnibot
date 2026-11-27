package larkauth

// sweeper.go — 授权收尾驱动(v2.13 IM 内闭环)。
//
// 背景:device flow 的收尾只由 Web 前端轮询触发,IM 里用户确认后没有任何东西去
// 问 CLI,于是永远停在 pending。本文件补上"谁去问"的驱动源。
//
// 协程纪律(这是本文件最容易被写坏的地方):
//   - 全局只有 1 个 ticker goroutine,不随待授权用户数增长;
//   - 每个用户一轮执行走 backtask.Gate 单飞,上一轮未完则跳过;
//   - Gate 的超时只取消 ctx、不杀 goroutine,所以内部每个阻塞调用必须吃 ctx,
//     否则槽位永远不释放,该用户从此再也不会被推进(逻辑泄漏);
//   - per-user 锁必须 defer 释放,panic 路径也要放。

import (
	"context"
	"time"

	"go.uber.org/zap"

	userdomain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/backtask"
	"omnibot/internal/pkg/crypto"
	"omnibot/pkg/logger"
)

const (
	// sweepInterval 收尾轮询节奏。device code 有效期 10 分钟,8s 一轮 → 用户确认后
	// 最迟 8 秒完成;没有 pending 行时这一轮只是一次很轻的 DB 查询。
	sweepInterval = 8 * time.Second
	// sweepBudget 单用户单轮总预算。必须覆盖 LoginComplete(5s) + 一次短身份查询,
	// 同时给 Gate 一个明确上界,避免任何"忘了传 ctx"的调用把槽位挂死。
	sweepBudget = 10 * time.Second
	// sweepDBTimeout 扫库超时:DB 卡住也必须让 ticker 继续转。
	sweepDBTimeout = 3 * time.Second
	// notifyTimeout 推送超时:推送失败不能拖住状态更新,更不能拖住收尾循环。
	notifyTimeout = 5 * time.Second
	// maxReissue 链接过期后自动换发上限。超过就停手等用户手动发起,防止无限刷链接。
	maxReissue = 1
)

// Notifier 授权结果推送(渠道无关)。channel/target 为空时由实现自行兜底到用户已绑渠道。
type Notifier interface {
	NotifyAuth(ctx context.Context, userID int64, channel, target, text string) error
}

// StartSweeper 启动收尾循环(进程级,阻塞;应在 RunDetached 中运行)。
func (s *Service) StartSweeper(ctx context.Context) {
	logger.InfoWithFields("larkauth: sweeper started",
		zap.Duration("interval", sweepInterval), zap.Duration("budget", s.budget))
	backtask.RunTickerLoop(ctx, sweepInterval, func(ctx context.Context) {
		s.SweepOnce(ctx)
	})
}

// SweepOnce 扫一轮 pending 授权。导出便于测试与手工触发。
func (s *Service) SweepOnce(ctx context.Context) {
	qctx, cancel := context.WithTimeout(ctx, sweepDBTimeout)
	defer cancel()

	rows, err := s.repo.ListByStatus(qctx, userdomain.LarkAuthStatusPending)
	if err != nil {
		logger.WarnWithFields("larkauth: sweep 扫库失败", zap.Error(err))
		return
	}
	for _, row := range rows {
		userID := row.UserID
		// 单飞:上一轮没跑完直接跳过(不排队、不叠跑)
		s.gate.Go(userID, s.budget, func(ctx context.Context) {
			s.sweepOne(ctx, userID)
		})
	}
}

// sweepOne 推进单个用户的 pending 授权。调用方保证同一用户单飞。
func (s *Service) sweepOne(ctx context.Context, userID int64) {
	mu := s.userLock(userID)
	if !mu.TryLock() {
		return // Web 正在收尾,下一轮再说
	}
	defer mu.Unlock() // panic 路径也必须释放,否则该用户永久卡死

	// 总预算收口:即使内部某个调用忘了传 ctx,也不会超出这一轮的上界
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()

	row, err := s.repo.GetByUserID(userID)
	if err != nil || row.Status != userdomain.LarkAuthStatusPending {
		return
	}
	if s.expired(row) {
		s.handleExpired(ctx, row)
		return
	}

	view, err := s.pollAuthLocked(ctx, userID)
	if err != nil {
		logger.WarnWithFields("larkauth: 收尾失败",
			zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	switch view.Status {
	case userdomain.LarkAuthStatusAuthorized:
		logger.InfoWithFields("larkauth: 授权收尾成功",
			zap.Int64("user_id", userID), zap.String("user_name", view.UserName))
		s.notify(ctx, row, BuildAuthSuccess(view.UserName))
	case userdomain.LarkAuthStatusFailed:
		logger.InfoWithFields("larkauth: 授权收尾失败(等用户重试)", zap.Int64("user_id", userID))
		s.notify(ctx, row, BuildAuthFailed())
	}
}

// handleExpired device code 过期:自动换发一次;再过期就停手,等用户手动发起。
func (s *Service) handleExpired(ctx context.Context, row *userdomain.LarkCLIAuth) {
	userID := row.UserID

	if row.ReissueCount >= maxReissue {
		row.Status = userdomain.LarkAuthStatusFailed
		row.DeviceCode, row.VerifiedURL = "", ""
		row.ExpiresAt = time.Time{}
		if err := s.repo.Update(row); err != nil {
			logger.WarnWithFields("larkauth: 标记授权过期失败",
				zap.Int64("user_id", userID), zap.Error(err))
			return
		}
		s.invalidateHealth(userID)
		s.notify(ctx, row, BuildAuthExpiredFinal())
		return
	}

	secret, err := crypto.Decrypt(row.AppSecretEnc)
	if err != nil {
		logger.WarnWithFields("larkauth: 换发时解密 secret 失败",
			zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	view, err := s.startAuth(ctx, userID, row.AppID, secret,
		row.NotifyChannel, row.NotifyTarget, row.ReissueCount+1)
	if err != nil {
		// 换发失败:标 failed,让用户手动重来(不无限重试)
		logger.WarnWithFields("larkauth: 自动换发失败",
			zap.Int64("user_id", userID), zap.Error(err))
		row.Status = userdomain.LarkAuthStatusFailed
		row.DeviceCode, row.VerifiedURL = "", ""
		row.ExpiresAt = time.Time{}
		_ = s.repo.Update(row)
		s.invalidateHealth(userID)
		s.notify(ctx, row, BuildAuthFailed())
		return
	}
	s.notify(ctx, row, BuildAuthReissued(view.VerifyURL))
}

// notify 推送授权结果。状态已经落库,推送失败只记日志——不能因为推送把流程带崩。
func (s *Service) notify(ctx context.Context, row *userdomain.LarkCLIAuth, text string) {
	if s.notifier == nil || text == "" {
		return
	}
	// 与收尾 ctx 解绑:ctx 可能已到期,但状态变更已经发生,通知要尽力发出去
	nctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), notifyTimeout)
	defer cancel()
	if err := s.notifier.NotifyAuth(nctx, row.UserID, row.NotifyChannel, row.NotifyTarget, text); err != nil {
		logger.WarnWithFields("larkauth: 授权通知发送失败",
			zap.Int64("user_id", row.UserID),
			zap.String("channel", row.NotifyChannel),
			zap.Error(err))
	}
}
