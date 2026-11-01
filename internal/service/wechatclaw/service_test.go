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
}

type sentMessage struct {
	to      string
	text    string
	ctxTkn  string
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
	if len(f.updates) == 0 {
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

	view, connected, err := svc.PollLogin(ctx, "qc-1", "")
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

	view, connected, err := svc.PollLogin(context.Background(), "qc-1", "1234")
	require.NoError(t, err)
	assert.False(t, connected)
	assert.True(t, view.NeedVerifyCode)
}

func TestPollLoginUnknownSession(t *testing.T) {
	svc := newTestService(t, &fakeRepo{}, &fakeClient{})
	_, _, err := svc.PollLogin(context.Background(), "ghost", "")
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

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "")
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
	_, _, err = svc.PollLogin(ctx, "qc-1", "")
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

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "")
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

	_, _, err = svc.PollLogin(context.Background(), "qc-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

var _ im.Sender = (*proto.Sender)(nil)
