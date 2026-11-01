// Package wechatclaw 提供微信 ClawBot 通道的业务服务:扫码登录、凭证保管、
// 长轮询运行态管理。
//
// 分层:服务层只依赖领域实体 + 协议层窄接口(ClientAPI),协议 HTTP 细节在
// internal/channel/wechatclaw;入站链路复用 internal/channel/im。
package wechatclaw

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"omnibot/internal/channel/im"
	channelfeishu "omnibot/internal/channel/wechatclaw"
	domainagent "omnibot/internal/domain/agent"
	domain "omnibot/internal/domain/wechatclaw"
	"omnibot/internal/pkg/backtask"
	"omnibot/internal/pkg/crypto"
	"omnibot/pkg/logger"
)

// 扫码会话有效期(官方插件 ACTIVE_LOGIN_TTL_MS = 5min)。
const qrSessionTTL = 5 * time.Minute

// channelSource 后台任务来源标记(子 Agent 完成后据此推回微信)。
const channelSource = domainagent.SourceWeChatClaw

// ErrQRExpired 扫码会话不存在或已过期(前端需重新申请二维码)。
var ErrQRExpired = errors.New("wechatclaw: qrcode session expired")

// ErrNoClient 未登录时调用需要 token 的能力。
var ErrNoClient = errors.New("wechatclaw: not connected")

// ClientAPI 协议客户端窄接口(测试用 fake 替换)。
type ClientAPI interface {
	GetBotQRCode(ctx context.Context) (*channelfeishu.QRCode, error)
	GetQRCodeStatus(ctx context.Context, qrcode, verifyCode string) (*channelfeishu.QRStatus, error)
	GetUpdates(ctx context.Context, buf string) (*channelfeishu.Updates, error)
	SendText(ctx context.Context, toUserID, text, contextToken, clientID string) (string, error)
	NotifyStart(ctx context.Context) error
	NotifyStop(ctx context.Context) error
}

// ClientFactory 按基址/token 构造客户端。token 为空 = 未登录(只用于扫码)。
type ClientFactory func(baseURL, token string) ClientAPI

// AccountRepository 凭证仓储(与 repository/wechatclaw 同构)。
type AccountRepository interface {
	Get() (*domain.Account, error)
	Save(account *domain.Account) error
	UpdateSyncBuf(id int64, buf string) error
	UpdateStatus(id int64, status string) error
	DeleteAll() error
}

// DirectBinder 扫码即绑定能力(service/user.BindingService 实现)。
// ClawBot 扫码的人就是机器人主人,不需要再发绑定码(31 号 §3.4)。
type DirectBinder interface {
	BindDirect(channelType string, userID int64, channelUserID string) error
}

// HandlerDeps 共享 IM pipeline 的依赖(绑定/落库/Agent/LLM)。
type HandlerDeps struct {
	Binding   im.BindingService
	Messages  im.MessageService
	Agent     im.AgentService
	LLMConfig im.LLMConfigService
}

// LoginView 扫码登录视图。
type LoginView struct {
	QRCode    string `json:"qrcode"`
	QRURL     string `json:"qr_url"`
	ExpiresIn int    `json:"expires_in"`
}

// PollView 扫码状态视图。
type PollView struct {
	Status         string `json:"status"`
	Connected      bool   `json:"connected"`
	NeedVerifyCode bool   `json:"need_verify_code"`
}

// StatusView 连接状态视图(不含任何凭证)。
type StatusView struct {
	Connected   bool   `json:"connected"`
	BotID       string `json:"bot_id,omitempty"`
	Status      string `json:"status,omitempty"`
	NeedRelogin bool   `json:"need_relogin,omitempty"`
}

// Service ClawBot 通道服务。
type Service struct {
	repo    AccountRepository
	factory ClientFactory
	deps    HandlerDeps

	// 扫码即绑定:登录发起人 ← 渠道身份(nil 时退化为需要绑定码)
	binder DirectBinder

	// 可选注入(与飞书同款能力,nil 时不启用)
	subAgentReporter im.SubAgentReportProvider
	memoryService    im.MemoryCommandService
	noteFeedbackSvc  im.NoteFeedbackService

	mu       sync.Mutex
	sessions map[string]time.Time
	tokens   map[string]string // peer(ilink user id) -> context_token
	runtime  *channelRuntime

	now func() time.Time
}

// channelRuntime 当前运行中的长轮询实例。
type channelRuntime struct {
	client ClientAPI
	sender *channelfeishu.Sender
	cancel context.CancelFunc
}

// NewService 创建服务。
func NewService(repo AccountRepository, factory ClientFactory, deps HandlerDeps) *Service {
	return &Service{
		repo:     repo,
		factory:  factory,
		deps:     deps,
		sessions: make(map[string]time.Time),
		tokens:   make(map[string]string),
		now:      time.Now,
	}
}

// SetDirectBinder 注入扫码直绑能力。nil 时保持「扫码后仍需绑定码」的旧行为。
func (s *Service) SetDirectBinder(b DirectBinder) { s.binder = b }

// SetSubAgentReporter 注入后台 Agent 前置汇报(08 §4.5)。
func (s *Service) SetSubAgentReporter(p im.SubAgentReportProvider) { s.subAgentReporter = p }

// SetMemoryService 注入记忆管理命令。
func (s *Service) SetMemoryService(svc im.MemoryCommandService) { s.memoryService = svc }

// SetNoteFeedbackService 注入主动消息反馈文本通道(29 号 N4)。
func (s *Service) SetNoteFeedbackService(svc im.NoteFeedbackService) { s.noteFeedbackSvc = svc }

// Start 服务启动时恢复:已有 active 账号则拉起长轮询。
func (s *Service) Start(ctx context.Context) {
	account, err := s.repo.Get()
	if err != nil {
		logger.ErrorWithFields("wechatclaw: load account failed", zap.Error(err))
		return
	}
	if account == nil {
		return
	}
	if !account.IsActive() {
		logger.InfoWithFields("wechatclaw: account expired, waiting for re-login",
			zap.String("bot_id", account.BotID))
		return
	}
	// 自愈:凭证在但绑定关系缺失(例如手动清过 user_channels)时补绑
	if account.LoginUserID > 0 {
		s.bindOwner(account, account.LoginUserID)
	}
	if err := s.startRuntime(ctx, account); err != nil {
		logger.ErrorWithFields("wechatclaw: start monitor failed", zap.Error(err))
	}
}

// StartLogin 申请登录二维码。
func (s *Service) StartLogin(ctx context.Context) (*LoginView, error) {
	client := s.factory(channelfeishu.DefaultBaseURL, "")
	qr, err := client.GetBotQRCode(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.sessions[qr.QRCode] = s.now()
	// 顺手清理过期会话,避免长期运行内存堆积
	for code, at := range s.sessions {
		if s.now().Sub(at) > qrSessionTTL {
			delete(s.sessions, code)
		}
	}
	s.mu.Unlock()

	return &LoginView{
		QRCode:    qr.QRCode,
		QRURL:     qr.ImgContent,
		ExpiresIn: int(qrSessionTTL.Seconds()),
	}, nil
}

// PollLogin 轮询扫码状态。confirmed 时落库凭证并拉起长轮询。
func (s *Service) PollLogin(ctx context.Context, qrcode, verifyCode string, loginUserID int64) (*PollView, bool, error) {
	if qrcode == "" {
		return nil, false, ErrQRExpired
	}
	s.mu.Lock()
	startedAt, ok := s.sessions[qrcode]
	s.mu.Unlock()
	if !ok || s.now().Sub(startedAt) > qrSessionTTL {
		return nil, false, ErrQRExpired
	}

	client := s.factory(channelfeishu.DefaultBaseURL, "")
	status, err := client.GetQRCodeStatus(ctx, qrcode, verifyCode)
	if err != nil {
		return nil, false, err
	}

	view := &PollView{Status: status.Status}
	switch status.Status {
	case channelfeishu.QRStatusNeedVerifyCode:
		view.NeedVerifyCode = true
	case channelfeishu.QRStatusConfirmed:
		account, err := s.saveAccount(status, loginUserID)
		if err != nil {
			return nil, false, err
		}
		// 扫码即绑定:扫的是谁的微信,就绑到发起扫码的 web 账号,不再要求发绑定码
		s.bindOwner(account, loginUserID)
		// 扫码成功后立即拉起长轮询,免重启
		if err := s.startRuntime(ctx, account); err != nil {
			logger.ErrorWithFields("wechatclaw: start monitor after login failed", zap.Error(err))
		}
		s.mu.Lock()
		delete(s.sessions, qrcode)
		s.mu.Unlock()
		view.Connected = true
	}
	return view, view.Connected, nil
}

// saveAccount 把 confirmed 状态写入凭证(覆盖旧账号,单账号语义)。
func (s *Service) saveAccount(status *channelfeishu.QRStatus, loginUserID int64) (*domain.Account, error) {
	if strings.TrimSpace(status.BotToken) == "" {
		return nil, fmt.Errorf("wechatclaw: confirmed without bot_token")
	}
	cipherText, err := crypto.Encrypt(status.BotToken)
	if err != nil {
		return nil, fmt.Errorf("wechatclaw: encrypt token: %w", err)
	}

	existing, err := s.repo.Get()
	if err != nil {
		return nil, err
	}
	account := &domain.Account{
		BotID:       status.BotID,
		ILinkUserID: status.UserID,
		LoginUserID: loginUserID,
		BaseURL:     status.BaseURL,
		TokenCipher: cipherText,
		Status:      domain.StatusActive,
	}
	if existing != nil {
		account.ID = existing.ID
		account.CreatedAt = existing.CreatedAt
		// 重新扫码 = 新会话,游标清零,避免旧游标错位
		account.SyncBuf = ""
	}
	if err := s.repo.Save(account); err != nil {
		return nil, err
	}
	return account, nil
}

// bindOwner 把机器人主人的渠道身份绑到发起扫码的 web 账号。
//
// 失败不阻断连接:扫码本身已成功,绑定关系可以在下次启动时由 Start 自愈补上。
func (s *Service) bindOwner(account *domain.Account, userID int64) {
	if s.binder == nil || userID <= 0 || account == nil || account.ILinkUserID == "" {
		return
	}
	if err := s.binder.BindDirect("wechat_claw", userID, account.ILinkUserID); err != nil {
		logger.WarnWithFields("wechatclaw: auto bind owner failed",
			zap.Int64("user_id", userID),
			zap.Error(err))
		return
	}
	logger.InfoWithFields("wechatclaw: owner bound",
		zap.Int64("user_id", userID))
}

// Status 当前连接状态。
func (s *Service) Status() (*StatusView, error) {
	account, err := s.repo.Get()
	if err != nil {
		return nil, err
	}
	if account == nil {
		return &StatusView{Connected: false}, nil
	}
	return &StatusView{
		Connected:   account.IsActive(),
		BotID:       account.BotID,
		Status:      account.Status,
		NeedRelogin: !account.IsActive(),
	}, nil
}

// Disconnect 停长轮询、通知服务端、清空凭证。
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	rt := s.runtime
	s.runtime = nil
	s.tokens = make(map[string]string)
	s.mu.Unlock()

	if rt != nil {
		if err := rt.client.NotifyStop(ctx); err != nil {
			logger.WarnWithFields("wechatclaw: notifystop failed", zap.Error(err))
		}
		rt.cancel()
	}
	return s.repo.DeleteAll()
}

// CurrentSender 返回当前运行中的发送器;nil 表示未连接(供 MessageChannel 提供方使用)。
func (s *Service) CurrentSender() *channelfeishu.Sender {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime == nil {
		return nil
	}
	return s.runtime.sender
}

// Token 实现 channel 层 TokenSource:取该对端的 context_token。
func (s *Service) Token(peer string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[peer]
}

// putToken 记录入站消息带来的 context_token(出站必须回传)。
func (s *Service) putToken(peer, token string) {
	if peer == "" || token == "" {
		return
	}
	s.mu.Lock()
	s.tokens[peer] = token
	s.mu.Unlock()
}

// startRuntime 构造 sender + im.Handler + monitor 并异步启动。
func (s *Service) startRuntime(ctx context.Context, account *domain.Account) error {
	token, err := crypto.Decrypt(account.TokenCipher)
	if err != nil {
		return fmt.Errorf("wechatclaw: decrypt token: %w", err)
	}
	baseURL := account.BaseURL
	if strings.TrimSpace(baseURL) == "" {
		baseURL = channelfeishu.DefaultBaseURL
	}
	client := s.factory(baseURL, token)

	sender := channelfeishu.NewSender(client, s)
	handler := im.NewHandler(im.Options{
		ChannelType: "wechat_claw",
		Source:      channelSource,
		Binding:     s.deps.Binding,
		Messages:    s.deps.Messages,
		Agent:       s.deps.Agent,
		LLMConfig:   s.deps.LLMConfig,
		Sender:      sender,

		BindSuccessReply:         "绑定成功!现在可以在微信跟我聊了",
		AlreadyBoundReply:        "该微信号已绑定其他账号",
		AccountAlreadyBoundReply: "你的账号已绑定微信,如需更换请稍后(暂不支持)",
		// ClawBot 是扫码即绑定,正常永远走不到这句;留作他人加到此机器人时的兜底
		UnboundGuide: "这个微信号还没有绑定 OmniBot 账号。请让主人在 Web 设置里重新扫码连接。",
	})
	if s.subAgentReporter != nil {
		handler.SetSubAgentReporter(s.subAgentReporter)
	}
	if s.memoryService != nil {
		handler.SetMemoryService(s.memoryService)
	}
	if s.noteFeedbackSvc != nil {
		handler.SetNoteFeedbackService(s.noteFeedbackSvc)
	}

	// 关键:长轮询生命周期不能跟随调用方的 ctx。
	// 扫码登录是由 HTTP 请求触发的,请求返回后 ctx 立刻取消——直接派生会让 monitor
	// 在登录成功的同一毫秒内退出(线上事故:notifystart context canceled、sync_buf 永远为空)。
	// WithoutCancel 切断取消与截止时间,后续只由 Disconnect / 进程退出取消。
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	rt := &channelRuntime{client: client, sender: sender, cancel: cancel}
	clearRuntime := func() {
		s.mu.Lock()
		// 只在「当前运行态就是自己」时清空,避免旧实例退出时误清新实例
		if s.runtime == rt {
			s.runtime = nil
		}
		s.mu.Unlock()
	}
	mon := newMonitor(monitorConfig{
		client:    client,
		handler:   handler,
		sender:    sender,
		tokens:    s,
		repo:      s.repo,
		accountID: account.ID,
		buf:       account.SyncBuf,
		onExit:    clearRuntime,
	})

	s.mu.Lock()
	old := s.runtime
	s.runtime = rt
	s.mu.Unlock()

	if old != nil {
		// 重扫/换号:先停旧长轮询,并通知服务端旧会话结束,避免两条连接同时收消息
		old.cancel()
		backtask.RunDetached("wechatclaw notify stop old session", func() {
			stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer stopCancel()
			if err := old.client.NotifyStop(stopCtx); err != nil {
				logger.WarnWithFields("wechatclaw: notifystop old session failed", zap.Error(err))
			}
		})
	}

	// 与飞书长连接同款纪律:goroutine 必须 recover——agent 链路一次 panic 不能打崩整个服务
	backtask.RunDetached("wechatclaw monitor", func() { mon.run(runCtx) })
	return nil
}
