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
)

// fakeHandler 记录入站调用。
type fakeHandler struct {
	mu    sync.Mutex
	calls []im.InboundMessage
	err   error
}

func (f *fakeHandler) HandleInbound(ctx context.Context, in im.InboundMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	return f.err
}

func (f *fakeHandler) getCalls() []im.InboundMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]im.InboundMessage{}, f.calls...)
}

// fakeSender 记录出站调用。
type fakeSender struct {
	mu   sync.Mutex
	sent []sentMessage
}

func (f *fakeSender) SendText(ctx context.Context, openID, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentMessage{to: openID, text: content})
	return nil
}

func (f *fakeSender) SendMarkdown(ctx context.Context, openID, content string) error {
	return f.SendText(ctx, openID, content)
}

func (f *fakeSender) SendCard(ctx context.Context, openID, title, content, template string) error {
	return f.SendText(ctx, openID, title+content)
}

func (f *fakeSender) messages() []sentMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentMessage{}, f.sent...)
}

func userMessage(id, from, text, ctxToken string) proto.Message {
	return proto.Message{
		MessageID:    proto.FlexString(id),
		FromUserID:   from,
		MessageType:  proto.MessageTypeUser,
		MessageState: proto.MessageStateFinish,
		ContextToken: ctxToken,
		ItemList:     []proto.MessageItem{{Type: proto.ItemTypeText, TextItem: &proto.TextItem{Text: text}}},
	}
}

func newTestMonitor(client ClientAPI, handler InboundHandler, sender im.Sender, repo AccountRepository, tokens TokenSink, onExpired func()) *monitor {
	return newMonitor(monitorConfig{
		client:       client,
		handler:      handler,
		sender:       sender,
		tokens:       tokens,
		repo:         repo,
		accountID:    1,
		onExpired:    onExpired,
		retryDelay:   time.Millisecond,
		backoffDelay: time.Millisecond,
	})
}

func TestMonitorDispatchTextMessage(t *testing.T) {
	client := &fakeClient{updates: []*proto.Updates{{
		Msgs:          []proto.Message{userMessage("42", "peer-1", "你好", "ctx-1")},
		GetUpdatesBuf: "buf-2",
	}}}
	handler := &fakeHandler{}
	repo := &fakeRepo{account: &domain.Account{ID: 1}}
	tokens := newTestService(t, repo, client)

	mon := newTestMonitor(client, handler, &fakeSender{}, repo, tokens, nil)
	mon.pollOnce(context.Background())

	calls := handler.getCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "peer-1", calls[0].OpenID)
	assert.Equal(t, "你好", calls[0].Text)
	assert.Equal(t, "42", calls[0].MsgID)
	assert.Equal(t, "p2p", calls[0].ChatType)

	// context_token 供出站使用
	assert.Equal(t, "ctx-1", tokens.Token("peer-1"))
	// 游标落库
	account, err := repo.Get()
	require.NoError(t, err)
	assert.Equal(t, "buf-2", account.SyncBuf)
}

func TestMonitorSkipsBotAndGroupMessages(t *testing.T) {
	botMsg := userMessage("1", "peer-1", "bot 自己", "ctx")
	botMsg.MessageType = proto.MessageTypeBot

	groupMsg := userMessage("2", "peer-2", "群里", "ctx")
	groupMsg.GroupID = "g-1"

	client := &fakeClient{updates: []*proto.Updates{{Msgs: []proto.Message{botMsg, groupMsg}}}}
	handler := &fakeHandler{}
	repo := &fakeRepo{account: &domain.Account{ID: 1}}
	svc := newTestService(t, repo, client)

	newTestMonitor(client, handler, &fakeSender{}, repo, svc, nil).pollOnce(context.Background())

	assert.Empty(t, handler.getCalls())
}

func TestMonitorNonTextSendsNotice(t *testing.T) {
	imageMsg := proto.Message{
		MessageID:   proto.FlexString("3"),
		FromUserID:  "peer-1",
		MessageType: proto.MessageTypeUser,
		ItemList:    []proto.MessageItem{{Type: proto.ItemTypeImage}},
	}
	client := &fakeClient{updates: []*proto.Updates{{Msgs: []proto.Message{imageMsg}}}}
	handler := &fakeHandler{}
	sender := &fakeSender{}
	repo := &fakeRepo{account: &domain.Account{ID: 1}}
	svc := newTestService(t, repo, client)

	newTestMonitor(client, handler, sender, repo, svc, nil).pollOnce(context.Background())

	assert.Empty(t, handler.getCalls())
	sent := sender.messages()
	require.Len(t, sent, 1)
	assert.Equal(t, unsupportedMessageReply, sent[0].text)
}

func TestMonitorSessionExpiredMarksAccount(t *testing.T) {
	client := &fakeClient{updErr: proto.ErrSessionExpired}
	handler := &fakeHandler{}
	repo := &fakeRepo{account: &domain.Account{ID: 1, Status: domain.StatusActive}}
	svc := newTestService(t, repo, client)

	expired := false
	mon := newTestMonitor(client, handler, &fakeSender{}, repo, svc, func() { expired = true })
	mon.pollOnce(context.Background())

	assert.True(t, expired)
	account, err := repo.Get()
	require.NoError(t, err)
	assert.Equal(t, domain.StatusExpired, account.Status)
}

func TestMonitorBackoffAfterConsecutiveFailures(t *testing.T) {
	client := &fakeClient{updErr: errors.New("network down")}
	repo := &fakeRepo{account: &domain.Account{ID: 1}}
	svc := newTestService(t, repo, client)
	mon := newTestMonitor(client, &fakeHandler{}, &fakeSender{}, repo, svc, nil)

	// 前两次失败只重试,第三次触发退避并把计数清零
	mon.pollOnce(context.Background())
	assert.Equal(t, 1, mon.failures)
	mon.pollOnce(context.Background())
	assert.Equal(t, 2, mon.failures)
	mon.pollOnce(context.Background())
	assert.Equal(t, 0, mon.failures)
}

func TestMonitorRunStopsOnContextCancel(t *testing.T) {
	client := &fakeClient{}
	repo := &fakeRepo{account: &domain.Account{ID: 1}}
	svc := newTestService(t, repo, client)
	mon := newTestMonitor(client, &fakeHandler{}, &fakeSender{}, repo, svc, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		mon.run(ctx)
		close(done)
	}()

	// 等到 notifystart 发出去,再取消
	waitFor(t, client.isStarted)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("monitor did not stop on cancel")
	}
	waitFor(t, client.isStopped)
}
