package wechatclaw

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/channel/im"
	proto "omnibot/internal/channel/wechatclaw"
	domain "omnibot/internal/domain/wechatclaw"
	"omnibot/internal/pkg/crypto"
)

// ===== fakes =====

type fakeClient struct {
	mu       sync.Mutex
	qr       *proto.QRCode
	qrErr    error
	qrStatus *proto.QRStatus
	qrStErr  error
	updates  []*proto.Updates
	updErr   error
	sent     []sentMessage
	started  bool
	stopped  bool
	// updatesCalls 记录 getupdates 实际调用次数(验证长轮询是否在跑)
	updatesCalls int
}

type sentMessage struct {
	to       string
	text     string
	ctxTkn   string
	clientID string
}

func (f *fakeClient) GetBotQRCode(ctx context.Context) (*proto.QRCode, error) {
	if f.qrErr != nil {
		return nil, f.qrErr
	}
	return f.qr, nil
}

func (f *fakeClient) GetQRCodeStatus(ctx context.Context, qrcode, verifyCode string) (*proto.QRStatus, error) {
	if f.qrStErr != nil {
		return nil, f.qrStErr
	}
	return f.qrStatus, nil
}

func (f *fakeClient) GetUpdates(ctx context.Context, buf string) (*proto.Updates, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updErr != nil {
		return nil, f.updErr
	}
	f.updatesCalls++
	if len(f.updates) == 0 {
		// 模拟真实长轮询的空转间隔,避免测试里忙轮询;同时响应 ctx 取消
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Millisecond):
		}
		return &proto.Updates{GetUpdatesBuf: buf}, nil
	}
	next := f.updates[0]
	f.updates = f.updates[1:]
	return next, nil
}

func (f *fakeClient) SendText(ctx context.Context, to, text, ctxToken, clientID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{to: to, text: text, ctxTkn: ctxToken, clientID: clientID})
	return "srv-1", nil
}

func (f *fakeClient) NotifyStart(ctx context.Context) error {
	f.mu.Lock()
	f.started = true
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) NotifyStop(ctx context.Context) error {
	f.mu.Lock()
	f.stopped = true
	f.mu.Unlock()
	return nil
}

func (f *fakeClient) updateCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.updatesCalls
}

func (f *fakeClient) isStarted() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started
}

func (f *fakeClient) isStopped() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.stopped
}

// fakeBinder 记录扫码直绑调用。
type fakeBinder struct {
	mu    sync.Mutex
	calls []bindCall
	err   error
}

type bindCall struct {
	channelType string
	userID      int64
	channelUser string
}

func (f *fakeBinder) BindDirect(channelType string, userID int64, channelUserID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, bindCall{channelType: channelType, userID: userID, channelUser: channelUserID})
	return f.err
}

func (f *fakeBinder) getCalls() []bindCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bindCall{}, f.calls...)
}

type fakeRepo struct {
	mu      sync.Mutex
	account *domain.Account
	seq     int64
}

func (r *fakeRepo) Get() (*domain.Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil {
		return nil, nil
	}
	cp := *r.account
	return &cp, nil
}

func (r *fakeRepo) Save(account *domain.Account) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if account.ID == 0 {
		r.seq++
		account.ID = r.seq
	}
	cp := *account
	r.account = &cp
	return nil
}

func (r *fakeRepo) UpdateSyncBuf(id int64, buf string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account != nil {
		r.account.SyncBuf = buf
	}
	return nil
}

func (r *fakeRepo) UpdateStatus(id int64, status string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account != nil {
		r.account.Status = status
	}
	return nil
}

func (r *fakeRepo) DeleteAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.account = nil
	return nil
}

// ===== helpers =====

func initCrypto(t *testing.T) {
	t.Helper()
	crypto.Init("wechatclaw-test-key")
}

func newTestService(t *testing.T, repo AccountRepository, client ClientAPI) *Service {
	t.Helper()
	initCrypto(t)
	svc := NewService(repo, func(baseURL, token string) ClientAPI { return client }, HandlerDeps{})
	svc.SetSubAgentReporter(nil)
	return svc
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

// ===== tests =====

func TestStartLoginReturnsQRURL(t *testing.T) {
	client := &fakeClient{qr: &proto.QRCode{QRCode: "qc-1", ImgContent: "https://liteapp.weixin.qq.com/q/x"}}
	svc := newTestService(t, &fakeRepo{}, client)

	view, err := svc.StartLogin(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "qc-1", view.QRCode)
	assert.Equal(t, "https://liteapp.weixin.qq.com/q/x", view.QRURL)
	assert.Equal(t, 300, view.ExpiresIn)
}

func TestPollLoginConfirmedSavesEncryptedTokenAndStartsMonitor(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1", ImgContent: "url"},
		qrStatus: &proto.QRStatus{
			Status:   proto.QRStatusConfirmed,
			BotToken: "bt-secret",
			BotID:    "bot-1",
			UserID:   "u-1",
			BaseURL:  "https://ilinkai.weixin.qq.com",
		},
	}
	repo := &fakeRepo{}
	svc := newTestService(t, repo, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)

	view, connected, err := svc.PollLogin(ctx, "qc-1", "", 0)
	require.NoError(t, err)
	assert.True(t, connected)
	assert.Equal(t, proto.QRStatusConfirmed, view.Status)

	account, err := repo.Get()
	require.NoError(t, err)
	require.NotNil(t, account)
	assert.Equal(t, "bot-1", account.BotID)
	assert.Equal(t, domain.StatusActive, account.Status)
	// token 必须密文入库
	assert.NotEqual(t, "bt-secret", account.TokenCipher)
	plain, err := crypto.Decrypt(account.TokenCipher)
	require.NoError(t, err)
	assert.Equal(t, "bt-secret", plain)

	status, err := svc.Status()
	require.NoError(t, err)
	assert.True(t, status.Connected)

	waitFor(t, client.isStarted)
}

func TestPollLoginNeedVerifyCode(t *testing.T) {
	client := &fakeClient{
		qr:       &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{Status: proto.QRStatusNeedVerifyCode},
	}
	svc := newTestService(t, &fakeRepo{}, client)
	_, err := svc.StartLogin(context.Background())
	require.NoError(t, err)

	view, connected, err := svc.PollLogin(context.Background(), "qc-1", "1234", 0)
	require.NoError(t, err)
	assert.False(t, connected)
	assert.True(t, view.NeedVerifyCode)
}

func TestPollLoginUnknownSession(t *testing.T) {
	svc := newTestService(t, &fakeRepo{}, &fakeClient{})
	_, _, err := svc.PollLogin(context.Background(), "ghost", "", 0)
	assert.ErrorIs(t, err, ErrQRExpired)
}

func TestPollLoginExpiredSession(t *testing.T) {
	client := &fakeClient{
		qr:       &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{Status: proto.QRStatusExpired},
	}
	svc := newTestService(t, &fakeRepo{}, client)
	_, err := svc.StartLogin(context.Background())
	require.NoError(t, err)
	// 手动把会话时间调老
	svc.mu.Lock()
	svc.sessions["qc-1"] = time.Now().Add(-10 * time.Minute)
	svc.mu.Unlock()

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "", 0)
	assert.ErrorIs(t, err, ErrQRExpired)
}

func TestDisconnectClearsAccount(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{
			Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1",
		},
	}
	repo := &fakeRepo{}
	svc := newTestService(t, repo, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)
	_, _, err = svc.PollLogin(ctx, "qc-1", "", 0)
	require.NoError(t, err)
	waitFor(t, client.isStarted)

	require.NoError(t, svc.Disconnect(context.Background()))
	assert.True(t, client.isStopped())

	account, err := repo.Get()
	require.NoError(t, err)
	assert.Nil(t, account)
	assert.Nil(t, svc.CurrentSender())
}

func TestStartResumesActiveAccount(t *testing.T) {
	initCrypto(t)
	cipherText, err := crypto.Encrypt("bt")
	require.NoError(t, err)
	repo := &fakeRepo{account: &domain.Account{
		ID: 1, BotID: "bot-1", TokenCipher: cipherText, Status: domain.StatusActive,
	}}
	client := &fakeClient{}
	svc := NewService(repo, func(baseURL, token string) ClientAPI { return client }, HandlerDeps{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	waitFor(t, client.isStarted)
	status, err := svc.Status()
	require.NoError(t, err)
	assert.True(t, status.Connected)
}

func TestStartSkipsExpiredAccount(t *testing.T) {
	repo := &fakeRepo{account: &domain.Account{ID: 1, BotID: "bot-1", Status: domain.StatusExpired}}
	client := &fakeClient{}
	svc := NewService(repo, func(baseURL, token string) ClientAPI { return client }, HandlerDeps{})

	svc.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	assert.False(t, client.isStarted())
}

func TestTokenCacheRoundTrip(t *testing.T) {
	svc := newTestService(t, &fakeRepo{}, &fakeClient{})
	assert.Empty(t, svc.Token("peer-1"))
	svc.putToken("peer-1", "ctx-1")
	assert.Equal(t, "ctx-1", svc.Token("peer-1"))
}

func TestSaveAccountWithoutTokenFails(t *testing.T) {
	client := &fakeClient{
		qr:       &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{Status: proto.QRStatusConfirmed, BotID: "bot-1"},
	}
	svc := newTestService(t, &fakeRepo{}, client)
	_, err := svc.StartLogin(context.Background())
	require.NoError(t, err)

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bot_token")
}

func TestPollLoginPropagatesClientError(t *testing.T) {
	client := &fakeClient{
		qr:      &proto.QRCode{QRCode: "qc-1"},
		qrStErr: errors.New("boom"),
	}
	svc := newTestService(t, &fakeRepo{}, client)
	_, err := svc.StartLogin(context.Background())
	require.NoError(t, err)

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "", 0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// TestMonitorSurvivesRequestContextCancel 回归:扫码登录由 HTTP 请求触发,
// 请求返回后 ctx 必然取消;monitor 不能跟着死,否则登录成功即断流(线上事故)。
func TestMonitorSurvivesRequestContextCancel(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{
			Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1",
		},
	}
	repo := &fakeRepo{}
	svc := newTestService(t, repo, client)

	// 用「请求级 ctx」完成扫码,然后立刻取消(模拟 gin 请求结束)
	reqCtx, cancelReq := context.WithCancel(context.Background())
	_, err := svc.StartLogin(reqCtx)
	require.NoError(t, err)
	_, _, err = svc.PollLogin(reqCtx, "qc-1", "", 42)
	require.NoError(t, err)
	cancelReq()

	waitFor(t, client.isStarted)
	before := client.updateCount()
	time.Sleep(60 * time.Millisecond)
	after := client.updateCount()

	assert.Greater(t, after, before, "monitor 不应随请求 ctx 取消而退出")
	assert.False(t, client.isStopped(), "请求结束不该触发 notifystop")
	require.NoError(t, svc.Disconnect(context.Background()))
}

// TestPollLoginBindsOwner 扫码即绑定:确认后直接把渠道身份绑到发起扫码的账号。
func TestPollLoginBindsOwner(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{
			Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1", UserID: "owner-1",
		},
	}
	repo := &fakeRepo{}
	binder := &fakeBinder{}
	svc := newTestService(t, repo, client)
	svc.SetDirectBinder(binder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)

	_, connected, err := svc.PollLogin(ctx, "qc-1", "", 42)
	require.NoError(t, err)
	assert.True(t, connected)

	calls := binder.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "wechat_claw", calls[0].channelType)
	assert.Equal(t, int64(42), calls[0].userID)
	assert.Equal(t, "owner-1", calls[0].channelUser)

	account, err := repo.Get()
	require.NoError(t, err)
	assert.Equal(t, int64(42), account.LoginUserID)
}

// TestBindOwnerFailureDoesNotBlockConnect 绑定失败不影响扫码结果(下次启动自愈)。
func TestBindOwnerFailureDoesNotBlockConnect(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{
			Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1", UserID: "owner-1",
		},
	}
	svc := newTestService(t, &fakeRepo{}, client)
	svc.SetDirectBinder(&fakeBinder{err: errors.New("db down")})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)
	_, connected, err := svc.PollLogin(ctx, "qc-1", "", 42)
	require.NoError(t, err)
	assert.True(t, connected)
}

// TestStartHealsMissingBinding 启动时凭证在、绑定关系缺失 -> 自动补绑。
func TestStartHealsMissingBinding(t *testing.T) {
	initCrypto(t)
	cipherText, err := crypto.Encrypt("bt")
	require.NoError(t, err)
	repo := &fakeRepo{account: &domain.Account{
		ID: 1, BotID: "bot-1", ILinkUserID: "owner-1", LoginUserID: 42,
		TokenCipher: cipherText, Status: domain.StatusActive,
	}}
	binder := &fakeBinder{}
	client := &fakeClient{}
	svc := NewService(repo, func(baseURL, token string) ClientAPI { return client }, HandlerDeps{})
	svc.SetDirectBinder(binder)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.Start(ctx)

	calls := binder.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, int64(42), calls[0].userID)
	assert.Equal(t, "owner-1", calls[0].channelUser)
}

var _ im.Sender = (*proto.Sender)(nil)

func TestStatusNoAccountAndExpired(t *testing.T) {
	svc := newTestService(t, &fakeRepo{}, &fakeClient{})
	st, err := svc.Status()
	require.NoError(t, err)
	assert.False(t, st.Connected)
	assert.Empty(t, st.BotID)

	// 过期账号:未连接 + 需要重新扫码
	repo := &fakeRepo{account: &domain.Account{ID: 1, BotID: "bot-1", Status: domain.StatusExpired}}
	svc2 := newTestService(t, repo, &fakeClient{})
	st2, err := svc2.Status()
	require.NoError(t, err)
	assert.False(t, st2.Connected)
	assert.True(t, st2.NeedRelogin)
	assert.Equal(t, domain.StatusExpired, st2.Status)
}

func TestStartWithBrokenCipherDoesNotStartMonitor(t *testing.T) {
	repo := &fakeRepo{account: &domain.Account{
		ID: 1, BotID: "bot-1", ILinkUserID: "owner-1",
		TokenCipher: "not-valid-ciphertext", Status: domain.StatusActive,
	}}
	client := &fakeClient{}
	svc := NewService(repo, func(baseURL, token string) ClientAPI { return client }, HandlerDeps{})

	svc.Start(context.Background())
	time.Sleep(20 * time.Millisecond)
	assert.False(t, client.isStarted(), "解密失败不应拉起长轮询")
}

func TestDisconnectWithoutRuntime(t *testing.T) {
	svc := newTestService(t, &fakeRepo{}, &fakeClient{})
	require.NoError(t, svc.Disconnect(context.Background()))
	assert.Nil(t, svc.CurrentSender())
}

func TestMonitorExpiredClearsRuntime(t *testing.T) {
	client := &fakeClient{
		qr: &proto.QRCode{QRCode: "qc-1"},
		qrStatus: &proto.QRStatus{
			Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1", UserID: "owner-1",
		},
	}
	repo := &fakeRepo{}
	svc := newTestService(t, repo, client)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)
	_, _, err = svc.PollLogin(ctx, "qc-1", "", 0)
	require.NoError(t, err)
	waitFor(t, func() bool { return svc.CurrentSender() != nil })

	// 模拟 token 失效:下一轮 getupdates 返回 -14
	client.mu.Lock()
	client.updErr = proto.ErrSessionExpired
	client.mu.Unlock()

	// 长轮询退出后运行态应被清理,不再显示「已连接」在跑
	waitFor(t, func() bool { return svc.CurrentSender() == nil })
	account, err := repo.Get()
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, account.Status)
}

// TestRestartReplacesOldRuntime 重扫/换号:旧长轮询必须停掉并通知服务端,不能两条连接并存。
func TestRestartReplacesOldRuntime(t *testing.T) {
	initCrypto(t)
	var mu sync.Mutex
	var clients []*fakeClient
	factory := func(baseURL, token string) ClientAPI {
		c := &fakeClient{
			qr: &proto.QRCode{QRCode: "qc-1"},
			qrStatus: &proto.QRStatus{
				Status: proto.QRStatusConfirmed, BotToken: "bt", BotID: "bot-1", UserID: "owner-1",
			},
		}
		mu.Lock()
		clients = append(clients, c)
		mu.Unlock()
		return c
	}
	svc := NewService(&fakeRepo{}, factory, HandlerDeps{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 第一次登录
	_, err := svc.StartLogin(ctx)
	require.NoError(t, err)
	_, _, err = svc.PollLogin(ctx, "qc-1", "", 0)
	require.NoError(t, err)

	mu.Lock()
	first := clients[len(clients)-1]
	mu.Unlock()
	waitFor(t, first.isStarted)

	// 第二次登录(重扫):应替换旧运行态
	_, err = svc.StartLogin(ctx)
	require.NoError(t, err)
	_, _, err = svc.PollLogin(ctx, "qc-1", "", 0)
	require.NoError(t, err)

	// 旧实例收到停止(NotifyStop)且不再被引用
	waitFor(t, first.isStopped)
	require.NoError(t, svc.Disconnect(context.Background()))
}
