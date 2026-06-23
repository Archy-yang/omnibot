package feishu

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainuser "omnibot/internal/domain/user"
	agentpkg "omnibot/internal/service/agent"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

// ===== Start 行为开关 =====

type mockStarter struct {
	started   bool
	returnErr error
}

func (m *mockStarter) Start(ctx context.Context) error {
	m.started = true
	return m.returnErr
}

// enabled=false: Start 应立即返回 nil,不触碰 starter(模拟开发态不启动飞书)。
func TestChannel_Start_Disabled_NoOp(t *testing.T) {
	starter := &mockStarter{}
	ch := NewChannel(Config{Enabled: false}, nil, nil, WithStarter(starter))
	err := ch.Start(context.Background())
	require.NoError(t, err)
	assert.False(t, starter.started, "disabled 时不应启动长连接")
}

// enabled=true 但凭证空: Start 应早失败,不进入长连接尝试。
func TestChannel_Start_Enabled_MissingCredentials(t *testing.T) {
	ch := NewChannel(Config{Enabled: true}, nil, nil, WithStarter(&mockStarter{}))
	err := ch.Start(context.Background())
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrMissingCredentials)
}

// enabled=true + 凭证齐: 应调 starter.Start。
func TestChannel_Start_Enabled_InvokesStarter(t *testing.T) {
	starter := &mockStarter{}
	ch := NewChannel(Config{Enabled: true, AppID: "cli_x", AppSecret: "s_x"}, nil, nil, WithStarter(starter))
	err := ch.Start(context.Background())
	require.NoError(t, err)
	assert.True(t, starter.started)
}

// starter 返回的 error 应原样上抛(测 SDK 启动失败时不被吞掉)。
func TestChannel_Start_PropagatesStarterError(t *testing.T) {
	starter := &mockStarter{returnErr: errors.New("ws dial timeout")}
	ch := NewChannel(Config{Enabled: true, AppID: "x", AppSecret: "y"}, nil, nil, WithStarter(starter))
	err := ch.Start(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ws dial timeout")
}

// ===== content 解析 =====

func TestExtractTextFromContent(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"happy", `{"text":"hello"}`, "hello"},
		{"empty content", "", ""},
		{"bad json", "not-json", ""},
		{"empty text", `{"text":""}`, ""},
		{"extra fields ignored", `{"text":"hi","mentions":[]}`, "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractTextFromContent(tc.in))
		})
	}
}

// ===== dispatchInbound:SDK 事件 → InboundMessage 翻译层 =====

func ptr(s string) *string { return &s }

// 正常 text 单聊事件应转出完整 InboundMessage 并被 handler 处理。
func TestDispatchInbound_TextP2P_TranslatesAndDispatches(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 7}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "好的"}}
	sender := &mockSender{}
	h := NewMessageHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{
				SenderId: &larkim.UserId{OpenId: ptr("ou_abc")},
			},
			Message: &larkim.EventMessage{
				MessageId:   ptr("om_1"),
				MessageType: ptr("text"),
				ChatType:    ptr("p2p"),
				Content:     ptr(`{"text":"你好"}`),
			},
		},
	}
	err := dispatchInbound(context.Background(), h, event)
	require.NoError(t, err)

	assert.Equal(t, "ou_abc", user.gotOpenID, "openID 应正确解析并传给 user service")
	assert.Equal(t, "你好", msg.savedUserContent, "text 应从 JSON content 提取")
	assert.Equal(t, "om_1", msg.savedUserMsgID, "message_id 应作 SaveUserMessage 的 msgID")
	assert.Equal(t, "ou_abc", sender.sentOpenID, "回复应发回原 openID")
}

// 非 text 消息(如 image)应被静默忽略。
func TestDispatchInbound_NonTextMessage_Ignored(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 7}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "ok"}}
	h := NewMessageHandler(user, msg, agent, &mockLLMConfigService{}, &mockSender{})

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("ou_x")}},
			Message: &larkim.EventMessage{MessageType: ptr("image"), ChatType: ptr("p2p"), Content: ptr(`{"image_key":"xx"}`)},
		},
	}
	err := dispatchInbound(context.Background(), h, event)
	require.NoError(t, err)
	assert.Equal(t, "", user.gotOpenID, "非 text 消息不应触发 user 创建")
}

// 群聊事件 chat_type=group 应被 handler 过滤(handler 测试已覆盖,这里再断翻译层透传 ChatType)。
func TestDispatchInbound_GroupChat_PassesChatTypeForFiltering(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 7}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "ok"}}
	sender := &mockSender{}
	h := NewMessageHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("ou_x")}},
			Message: &larkim.EventMessage{MessageType: ptr("text"), ChatType: ptr("group"), Content: ptr(`{"text":"hi"}`)},
		},
	}
	err := dispatchInbound(context.Background(), h, event)
	require.NoError(t, err)
	assert.Equal(t, 0, agent.callCount, "群聊应被 handler 过滤")
	assert.Equal(t, 0, sender.sendCount)
}

// 缺字段的事件应被安全忽略不 panic。
func TestDispatchInbound_NilOrMissingFields_NoPanic(t *testing.T) {
	h := NewMessageHandler(&mockUserService{}, &mockMessageService{}, &mockAgentService{}, &mockLLMConfigService{}, &mockSender{})

	// 完全空事件
	require.NoError(t, dispatchInbound(context.Background(), h, nil))

	// Event 为空
	require.NoError(t, dispatchInbound(context.Background(), h, &larkim.P2MessageReceiveV1{}))

	// Message 为空
	require.NoError(t, dispatchInbound(context.Background(), h, &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender: &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("ou_x")}},
		},
	}))

	// MessageType 为 nil
	require.NoError(t, dispatchInbound(context.Background(), h, &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("ou_x")}},
			Message: &larkim.EventMessage{ChatType: ptr("p2p")},
		},
	}))
}

// ===== MessageChannel 接口契约 =====

// SendText/SendReply 通过 Sender 转发。
func TestChannel_SendText_ViaSender(t *testing.T) {
	sender := &mockSender{}
	ch := NewChannel(Config{Enabled: false}, nil, sender)
	err := ch.SendText("ou_x", "hi")
	require.NoError(t, err)
	assert.Equal(t, "ou_x", sender.sentOpenID)
	assert.Equal(t, "hi", sender.sentContent)
}

func TestChannel_ChannelType(t *testing.T) {
	ch := NewChannel(Config{Enabled: false}, nil, nil)
	assert.Equal(t, "feishu", ch.ChannelType())
	assert.True(t, ch.IsAsync())
}
