package wechatclaw

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.uber.org/zap"

	"omnibot/internal/channel/im"
	channelfeishu "omnibot/internal/channel/wechatclaw"
	domain "omnibot/internal/domain/wechatclaw"
	"omnibot/pkg/logger"
)

// 长轮询节奏(与官方插件一致:连续失败 3 次退避 30s,其余 2s 重试)。
const (
	maxConsecutiveFailures = 3
	defaultRetryDelay      = 2 * time.Second
	defaultBackoffDelay    = 30 * time.Second
	// unsupportedMessageReply 非文本消息的固定回执(本期只支持文本)。
	unsupportedMessageReply = "暂时只支持文字消息，图片/语音/文件稍后支持。"
)

// InboundHandler 入站处理接口(im.Handler 满足)。
type InboundHandler interface {
	HandleInbound(ctx context.Context, in im.InboundMessage) error
}

// TokenSink 记录入站消息带来的 context_token。
type TokenSink interface {
	putToken(peer, token string)
}

// monitorConfig 长轮询实例配置(字段可注入,便于测试压缩时延)。
type monitorConfig struct {
	client    ClientAPI
	handler   InboundHandler
	sender    im.Sender
	tokens    TokenSink
	repo      AccountRepository
	accountID int64
	buf       string
	// onExit 循环退出时调用(正常停止/会话失效/panic 被 recover 都算),
	// 用于清运行态,避免 Status 继续显示「已连接」。
	onExit func()

	retryDelay   time.Duration
	backoffDelay time.Duration
}

// monitor 长轮询循环。
type monitor struct {
	cfg      monitorConfig
	failures int
	polls    int
}

// pollHeartbeatInterval 每 N 轮成功拉取打一次存活日志(poll 间隔约 35s)。
const pollHeartbeatInterval = 20

func newMonitor(cfg monitorConfig) *monitor {
	if cfg.retryDelay <= 0 {
		cfg.retryDelay = defaultRetryDelay
	}
	if cfg.backoffDelay <= 0 {
		cfg.backoffDelay = defaultBackoffDelay
	}
	if cfg.onExit == nil {
		cfg.onExit = func() {}
	}
	return &monitor{cfg: cfg}
}

// run 阻塞运行直到 ctx 取消或会话失效。
func (m *monitor) run(ctx context.Context) {
	logger.InfoWithFields("wechatclaw: monitor started", zap.Int64("account_id", m.cfg.accountID))
	// 退出必须留痕:上一版因 ctx 提前取消静默退出,线上排查时完全看不出循环已死
	defer func() {
		m.cfg.onExit()
		logger.InfoWithFields("wechatclaw: monitor stopped",
			zap.Int64("account_id", m.cfg.accountID),
			zap.Bool("ctx_canceled", ctx.Err() != nil),
			zap.Int("failures", m.failures))
	}()
	if err := m.cfg.client.NotifyStart(ctx); err != nil {
		logger.WarnWithFields("wechatclaw: notifystart failed", zap.Error(err))
	}
	// 退出时尽最大努力通知服务端(用独立 ctx,避免被已取消的 ctx 打断)
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := m.cfg.client.NotifyStop(stopCtx); err != nil {
			logger.WarnWithFields("wechatclaw: notifystop on exit failed", zap.Error(err))
		}
	}()

	for {
		if ctx.Err() != nil {
			return
		}
		if stop := m.pollOnce(ctx); stop {
			return
		}
	}
}

// pollOnce 执行一轮拉取 + 分发。返回 stop=true 表示循环应终止(会话失效)。
func (m *monitor) pollOnce(ctx context.Context) (stop bool) {
	updates, err := m.cfg.client.GetUpdates(ctx, m.cfg.buf)
	if err != nil {
		if errors.Is(err, channelfeishu.ErrSessionExpired) {
			// -14:会话失效,标记账号过期并停止轮询,等用户重新扫码
			logger.WarnWithFields("wechatclaw: session expired, monitor stopped",
				zap.Int64("account_id", m.cfg.accountID))
			_ = m.cfg.repo.UpdateStatus(m.cfg.accountID, domain.StatusExpired)
			return true
		}
		m.failures++
		wait := m.cfg.retryDelay
		if m.failures >= maxConsecutiveFailures {
			wait = m.cfg.backoffDelay
			m.failures = 0
			logger.WarnWithFields("wechatclaw: getupdates failing, backing off",
				zap.Duration("delay", wait), zap.Error(err))
		} else {
			logger.WarnWithFields("wechatclaw: getupdates failed, retrying",
				zap.Duration("delay", wait), zap.Error(err))
		}
		sleepCtx(ctx, wait)
		return false
	}

	m.failures = 0
	m.polls++
	if m.polls%pollHeartbeatInterval == 0 {
		logger.InfoWithFields("wechatclaw: poll alive",
			zap.Int64("account_id", m.cfg.accountID),
			zap.Int("polls", m.polls))
	}
	// 游标推进并落库(重启续传)
	if updates.GetUpdatesBuf != "" && updates.GetUpdatesBuf != m.cfg.buf {
		m.cfg.buf = updates.GetUpdatesBuf
		if err := m.cfg.repo.UpdateSyncBuf(m.cfg.accountID, m.cfg.buf); err != nil {
			logger.WarnWithFields("wechatclaw: save sync buf failed", zap.Error(err))
		}
	}

	for _, msg := range updates.Msgs {
		m.dispatch(ctx, msg)
	}
	return false
}

// dispatch 单条消息分发。只处理单聊里的文本,其余回固定提示。
func (m *monitor) dispatch(ctx context.Context, msg channelfeishu.Message) {
	// 只处理用户消息(BOT 自己的消息/工具调用回显都跳过)
	if msg.MessageType != channelfeishu.MessageTypeUser {
		return
	}
	// 群聊本期不处理
	if msg.GroupID != "" {
		return
	}
	peer := msg.FromUserID
	if peer == "" {
		return
	}
	// context_token 是回消息的会话锚点,必须在分发前记录
	m.cfg.tokens.putToken(peer, msg.ContextToken)

	// 排障锚点:入站是否真的到了、有没有会话锚点(不记录消息内容,守隐私红线)
	logger.InfoWithFields("wechatclaw: inbound message",
		zap.String("peer", peer),
		zap.String("message_id", msg.MessageID.String()),
		zap.Int("text_len", len([]rune(msg.Text()))),
		zap.Bool("has_context_token", msg.ContextToken != ""),
	)

	text := strings.TrimSpace(msg.Text())
	if text == "" {
		if err := m.cfg.sender.SendText(ctx, peer, unsupportedMessageReply); err != nil {
			logger.WarnWithFields("wechatclaw: send unsupported notice failed",
				zap.String("peer", peer), zap.Error(err))
		}
		return
	}

	if err := m.cfg.handler.HandleInbound(ctx, im.InboundMessage{
		OpenID:   peer,
		Text:     text,
		MsgID:    msg.MessageID.String(),
		ChatType: "p2p",
	}); err != nil {
		logger.ErrorWithFields("wechatclaw: handle inbound failed",
			zap.String("peer", peer), zap.Error(err))
	}
}

// sleepCtx 可被 ctx 中断的 sleep。返回 false 表示 ctx 已取消。
func sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
