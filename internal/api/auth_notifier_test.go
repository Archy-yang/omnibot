package api

// auth_notifier_test.go — 授权结果回推 IM(v2.13)。
//
// 这个组件决定"用户确认后到底有没有收到消息",分支不少:
// Web 发起不推、指定渠道直发、渠道全挂时回落、没有绑定则静默。
// 上线前必须逐条锁住,否则最容易的失败模式是"授权成功了但用户不知道"。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	channelfactory "omnibot/internal/channel"
	userdomain "omnibot/internal/domain/user"
)

type sentMsg struct {
	channelType string
	target      string
	text        string
}

// fakeMessageChannel 注册进 channelfactory 的假渠道。
type fakeMessageChannel struct {
	typ   string
	async bool
	err   error
	sink  *[]sentMsg
}

func (f *fakeMessageChannel) ChannelType() string { return f.typ }
func (f *fakeMessageChannel) IsAsync() bool       { return f.async }
func (f *fakeMessageChannel) SendText(channelUserID, content string) error {
	*f.sink = append(*f.sink, sentMsg{channelType: f.typ, target: channelUserID, text: content})
	return f.err
}
func (f *fakeMessageChannel) SendReply(_, channelUserID, content string) error {
	return f.SendText(channelUserID, content)
}

// registerFakeChannel 注册一个假渠道(每个用例用不同 type,避免互相串)。
func registerFakeChannel(t *testing.T, typ string, async bool, err error, sink *[]sentMsg) *fakeMessageChannel {
	t.Helper()
	ch := &fakeMessageChannel{typ: typ, async: async, err: err, sink: sink}
	channelfactory.Register(ch)
	return ch
}

// fakeChannelLister userChannelLister 的桩。
type fakeChannelLister struct {
	rows []*userdomain.UserChannel
	err  error
}

func (f *fakeChannelLister) GetByUserID(int64) ([]*userdomain.UserChannel, error) {
	return f.rows, f.err
}

func boundTo(userID int64, channelType, channelUserID string) *userdomain.UserChannel {
	return &userdomain.UserChannel{UserID: userID, ChannelType: channelType, ChannelUserID: channelUserID}
}

// T1 发起时记录了 channel/target → 精确投递到那个会话。
func TestAuthNotifier_PrefersRecordedTarget(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_direct", true, nil, &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "test_auth_direct", "ou_peer", "授权已恢复"))
	require.Len(t, sent, 1)
	assert.Equal(t, "ou_peer", sent[0].target)
	assert.Equal(t, "授权已恢复", sent[0].text)
}

// T2 Web 发起的授权不推 IM:设置页自己会轮询,再推一条只会让用户困惑。
func TestAuthNotifier_WebSourceDoesNotPush(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_web", true, nil, &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{rows: []*userdomain.UserChannel{
		boundTo(7, "test_auth_web", "ou_should_not_get"),
	}})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "web", "", "授权已恢复"))
	assert.Empty(t, sent, "Web 发起不该往 IM 推")
}

// T3 没记录渠道(如旧数据)→ 回落 user_channels 里第一个可异步发送的绑定。
func TestAuthNotifier_FallsBackToBoundChannel(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_sync", false, nil, &sent)   // 同步渠道,应跳过
	registerFakeChannel(t, "test_auth_async", true, nil, &sent)    // 异步渠道,命中
	n := newChannelAuthNotifier(&fakeChannelLister{rows: []*userdomain.UserChannel{
		boundTo(7, "test_auth_sync", "u-sync"),
		boundTo(7, "test_auth_async", "u-async"),
	}})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "", "", "授权已恢复"))
	require.Len(t, sent, 1)
	assert.Equal(t, "test_auth_async", sent[0].channelType, "同步渠道不可主动发送,必须跳过")
	assert.Equal(t, "u-async", sent[0].target)
}

// T4 一个渠道发送失败 → 继续试下一个(不能因为一个绑定的问题吞掉通知)。
func TestAuthNotifier_FallbackSkipsFailingChannel(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_bad", true, errors.New("channel offline"), &sent)
	registerFakeChannel(t, "test_auth_good", true, nil, &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{rows: []*userdomain.UserChannel{
		boundTo(7, "test_auth_bad", "u1"),
		boundTo(7, "test_auth_good", "u2"),
	}})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "", "", "授权已恢复"))
	require.Len(t, sent, 2, "两个渠道都应尝试")
	assert.Equal(t, "u2", sent[1].target)
}

// T5 没有任何绑定 → 静默成功(状态已落库,不算错误)。
func TestAuthNotifier_NoBindingIsSilent(t *testing.T) {
	var sent []sentMsg
	n := newChannelAuthNotifier(&fakeChannelLister{})
	require.NoError(t, n.NotifyAuth(context.Background(), 7, "", "", "授权已恢复"))
	assert.Empty(t, sent)
}

// T6 指定渠道发送失败 → 返回错误(调用方记日志,状态不回滚)。
func TestAuthNotifier_RecordedChannelErrorIsReturned(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_err", true, errors.New("boom"), &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{})

	err := n.NotifyAuth(context.Background(), 7, "test_auth_err", "ou_peer", "授权已恢复")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "test_auth_err")
}

// T7 查绑定出错 → 返回错误,不 panic。
func TestAuthNotifier_ListerError(t *testing.T) {
	n := newChannelAuthNotifier(&fakeChannelLister{err: errors.New("db down")})
	err := n.NotifyAuth(context.Background(), 7, "", "", "授权已恢复")
	require.Error(t, err)
}

// T8 空文案不发。
func TestAuthNotifier_EmptyTextIsNoop(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_empty", true, nil, &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "test_auth_empty", "ou_peer", ""))
	assert.Empty(t, sent)
}

// 未注入 user_channels 仓储(装配缺失)→ 静默,不 panic。
func TestAuthNotifier_NilListerIsSilent(t *testing.T) {
	n := newChannelAuthNotifier(nil)
	require.NoError(t, n.NotifyAuth(context.Background(), 7, "", "", "授权已恢复"))
}

// 记录的渠道未注册(旧数据/渠道下线)→ 回落到已绑定渠道,而不是丢消息。
func TestAuthNotifier_UnknownRecordedChannelFallsBack(t *testing.T) {
	var sent []sentMsg
	registerFakeChannel(t, "test_auth_fallback", true, nil, &sent)
	n := newChannelAuthNotifier(&fakeChannelLister{rows: []*userdomain.UserChannel{
		boundTo(7, "test_auth_fallback", "u-fb"),
	}})

	require.NoError(t, n.NotifyAuth(context.Background(), 7, "gone_channel", "u-gone", "授权已恢复"))
	require.Len(t, sent, 1)
	assert.Equal(t, "u-fb", sent[0].target)
}
