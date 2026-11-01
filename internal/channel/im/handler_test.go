package im

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	usersvc "omnibot/internal/service/user"
)

// fakeBinding 记录 BindChannel/ResolveUserID 的调用参数。
type fakeBinding struct {
	bindCalled     bool
	bindChannel    string
	bindCode       string
	bindOpenID     string
	bindErr        error
	resolveUserID  int64
	resolveBound   bool
	resolveErr     error
	resolveChannel string
}

func (f *fakeBinding) BindChannel(channelType, code, openID string) error {
	f.bindCalled = true
	f.bindChannel, f.bindCode, f.bindOpenID = channelType, code, openID
	return f.bindErr
}

func (f *fakeBinding) ResolveUserID(channelType, openID string) (int64, bool, error) {
	f.resolveChannel = channelType
	return f.resolveUserID, f.resolveBound, f.resolveErr
}

// fakeSender 记录出站文本。
type fakeSender struct {
	sent []string
}

func (f *fakeSender) SendText(ctx context.Context, openID, content string) error {
	f.sent = append(f.sent, content)
	return nil
}
func (f *fakeSender) SendMarkdown(ctx context.Context, openID, content string) error {
	return f.SendText(ctx, openID, content)
}
func (f *fakeSender) SendCard(ctx context.Context, openID, title, content, template string) error {
	return f.SendText(ctx, openID, title+content)
}

func newBindHandler(binding BindingService, sender Sender) *Handler {
	return NewHandler(Options{
		ChannelType: "wechat_claw",
		Source:      "wechat_claw",
		Binding:     binding,
		Sender:      sender,

		BindSuccessReply:         "绑定成功!现在可以在微信跟我聊了",
		AlreadyBoundReply:        "该微信号已绑定其他账号",
		AccountAlreadyBoundReply: "你的账号已绑定微信,如需更换请稍后(暂不支持)",
	})
}

func TestHandleInboundBindCodeUsesChannelType(t *testing.T) {
	binding := &fakeBinding{}
	sender := &fakeSender{}
	h := newBindHandler(binding, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "绑定 123456", MsgID: "m1", ChatType: "p2p",
	})
	require.NoError(t, err)

	require.True(t, binding.bindCalled)
	// 渠道类型必须由 Options 注入,不能写死成 feishu
	assert.Equal(t, "wechat_claw", binding.bindChannel)
	assert.Equal(t, "123456", binding.bindCode)
	assert.Equal(t, "peer-1", binding.bindOpenID)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "绑定成功!现在可以在微信跟我聊了", sender.sent[0])
}

func TestHandleInboundBindCodeErrorsMapToReplies(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"invalid code", usersvc.ErrCodeInvalid, "绑定码无效或已过期,请在 web 端重新获取"},
		{"channel bound", usersvc.ErrChannelAlreadyBound, "该微信号已绑定其他账号"},
		{"account bound", usersvc.ErrAccountAlreadyBound, "你的账号已绑定微信,如需更换请稍后(暂不支持)"},
		{"unknown", errors.New("boom"), "服务暂时不可用,请稍后再试"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sender := &fakeSender{}
			h := newBindHandler(&fakeBinding{bindErr: tc.err}, sender)

			require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
				OpenID: "peer-1", Text: "绑定 123456", MsgID: "m1", ChatType: "p2p",
			}))
			require.Len(t, sender.sent, 1)
			assert.Equal(t, tc.want, sender.sent[0])
		})
	}
}

func TestHandleInboundUnboundSendsGuideWithoutAgent(t *testing.T) {
	binding := &fakeBinding{resolveBound: false}
	sender := &fakeSender{}
	h := newBindHandler(binding, sender)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "今天有什么安排", MsgID: "m1", ChatType: "p2p",
	}))

	assert.Equal(t, "wechat_claw", binding.resolveChannel)
	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0], "绑定码")
}

func TestHandleInboundSkipsGroupAndEmpty(t *testing.T) {
	sender := &fakeSender{}
	h := newBindHandler(&fakeBinding{}, sender)

	// 群聊本期不处理
	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "群里说话", MsgID: "m1", ChatType: "group",
	}))
	// 空文本不触发
	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "   ", MsgID: "m2", ChatType: "p2p",
	}))

	assert.Empty(t, sender.sent)
}
