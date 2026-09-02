package feishu

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentpkg "omnibot/internal/service/agent"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
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
	binding := &mockBindingService{resolveUserID: 7, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "好的"}}
	sender := &mockSender{}
	h := NewMessageHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

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

	// v2.2: binding 不再记录 openID,断言改为 msg/sender 落点
	assert.Equal(t, "你好", msg.savedUserContent, "text 应从 JSON content 提取")
	assert.Equal(t, "om_1", msg.savedUserMsgID, "message_id 应作 SaveUserMessage 的 msgID")
	assert.Equal(t, "ou_abc", sender.sentOpenID, "回复应发回原 openID")
}

// 非 text 消息(如 image)应被静默忽略。
func TestDispatchInbound_NonTextMessage_Ignored(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 7, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "ok"}}
	h := NewMessageHandler(binding, msg, agent, &mockLLMConfigService{}, &mockSender{})

	event := &larkim.P2MessageReceiveV1{
		Event: &larkim.P2MessageReceiveV1Data{
			Sender:  &larkim.EventSender{SenderId: &larkim.UserId{OpenId: ptr("ou_x")}},
			Message: &larkim.EventMessage{MessageType: ptr("image"), ChatType: ptr("p2p"), Content: ptr(`{"image_key":"xx"}`)},
		},
	}
	err := dispatchInbound(context.Background(), h, event)
	require.NoError(t, err)
	assert.Equal(t, "", msg.savedUserContent, "非 text 消息不应触发对话")
}

// 群聊事件 chat_type=group 应被 handler 过滤(handler 测试已覆盖,这里再断翻译层透传 ChatType)。
func TestDispatchInbound_GroupChat_PassesChatTypeForFiltering(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 7, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "ok"}}
	sender := &mockSender{}
	h := NewMessageHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

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
	h := NewMessageHandler(&mockBindingService{resolveBound: true, resolveUserID: 7}, &mockMessageService{}, &mockAgentService{}, &mockLLMConfigService{}, &mockSender{})

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

// ===== 未识别事件 dump =====
//
// 飞书平台会推送一些我们当前不处理的事件(例如「用户进入机器人对话」
// `im.chat.access_event.bot_p2p_chat_entered_v1`)。SDK 默认行为是打 ERROR
// 日志 `event type: xxx, not found handler`,但**不打事件 payload**——排查
// 时只能猜事件含义。
//
// 解决:维护一份"已知但不处理"事件白名单 chatterEventTypes,启动时全部注册
// 一个 dump handler——把事件 type + 原始 payload 输出到 zap INFO 日志。
// 收益:
//   - SDK 不再打 ERROR(我们处理了)
//   - 事件原文可见,后续若需新增业务逻辑,直接照着 payload 实现即可
//   - 任何"新"的杂音事件依然走 SDK 原路径打 not found handler ERROR,
//     提示开发者扩白名单(白名单是显式行为,不会无声吞掉新事件)
func TestRegisterUnhandledEventDumper_RegistersAllListedTypes(t *testing.T) {
	disp := dispatcher.NewEventDispatcher("", "")
	types := []string{
		"im.chat.access_event.bot_p2p_chat_entered_v1",
		"im.chat.member.user.added_v1",
	}
	registerUnhandledEventDumper(disp, types)

	// SDK 内部用 DoHandle 路由事件;没注册时返回 NotFoundEventHandlerErr,
	// 注册成功时找得到 handler 并返回 success body。我们用 Do 接口投喂一个
	// 模拟 payload,断言两件事:不报 not-found 错;响应 200。
	for _, evType := range types {
		t.Run(evType, func(t *testing.T) {
			payload := []byte(`{"schema":"2.0","header":{"event_type":"` + evType + `","app_id":"cli_test"},"event":{"foo":"bar"}}`)
			req := &larkevent.EventReq{
				Header: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: payload,
			}
			resp := disp.Handle(context.Background(), req)
			require.NotNil(t, resp)
			assert.Equal(t, 200, resp.StatusCode)
			// success body 含字符串 "success";若未注册 handler,body 含 "not found handler"
			assert.NotContains(t, string(resp.Body), "not found handler",
				"dispatcher should NOT report missing handler after registration")
		})
	}
}

// 反向验证:不在白名单的事件类型仍然会被 SDK 报 not found——
// 保证白名单是显式行为,不会吞掉新事件。
func TestRegisterUnhandledEventDumper_UnlistedEventStillReportsNotFound(t *testing.T) {
	disp := dispatcher.NewEventDispatcher("", "")
	registerUnhandledEventDumper(disp, []string{"im.chat.access_event.bot_p2p_chat_entered_v1"})

	// 投喂一个白名单外的事件类型
	payload := []byte(`{"schema":"2.0","header":{"event_type":"never.seen.before","app_id":"cli_test"},"event":{}}`)
	req := &larkevent.EventReq{
		Header: map[string][]string{"Content-Type": {"application/json"}},
		Body:   payload,
	}
	resp := disp.Handle(context.Background(), req)
	require.NotNil(t, resp)
	assert.Contains(t, string(resp.Body), "not found handler",
		"unlisted event types must still surface the SDK's not-found error so developers add them to the allowlist")
}
