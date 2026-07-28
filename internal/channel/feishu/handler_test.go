package feishu

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	userservice "omnibot/internal/service/user"
)

// ===== Mocks =====

type mockBindingService struct {
	// ResolveFeishuUserID 行为
	resolveUserID int64
	resolveBound  bool
	resolveErr    error
	// BindFeishu 行为
	bindErr       error
	bindCalled    bool
	bindGotCode   string
	bindGotOpenID string
}

func (m *mockBindingService) BindChannel(channelType, code, openID string) error {
	m.bindCalled = true
	m.bindGotCode, m.bindGotOpenID = code, openID
	return m.bindErr
}

func (m *mockBindingService) ResolveUserID(channelType, openID string) (int64, bool, error) {
	return m.resolveUserID, m.resolveBound, m.resolveErr
}

type mockMessageService struct {
	savedUserContent   string
	savedUserMsgID     string
	saveUserErr        error
	savedAsstContent   string
	savedSegments      []conversation.MessageSegment
	savedSteps         []*conversation.AgentStep
	saveAsstCalled     int
	buildContextResult []llm.ChatMessage
	buildContextErr    error
}

func (m *mockMessageService) SaveUserMessage(ctx context.Context, userID int64, content, msgID string) error {
	m.savedUserContent, m.savedUserMsgID = content, msgID
	return m.saveUserErr
}
func (m *mockMessageService) BuildContextMessages(ctx context.Context, userID int64, current string) ([]llm.ChatMessage, error) {
	if m.buildContextErr != nil {
		return nil, m.buildContextErr
	}
	if m.buildContextResult != nil {
		return m.buildContextResult, nil
	}
	return []llm.ChatMessage{{Role: "user", Content: current}}, nil
}
func (m *mockMessageService) SaveAssistantMessageWithSegments(
	ctx context.Context, userID int64, content string,
	segments []conversation.MessageSegment, steps []*conversation.AgentStep,
) error {
	m.saveAsstCalled++
	m.savedAsstContent = content
	m.savedSegments = segments
	m.savedSteps = steps
	return nil
}

func (m *mockMessageService) SaveAssistantMessageWithToolCalls(
	ctx context.Context, userID int64, content string,
	segments []conversation.MessageSegment, toolCalls *string, steps []*conversation.AgentStep,
) error {
	m.saveAsstCalled++
	m.savedAsstContent = content
	m.savedSegments = segments
	m.savedSteps = steps
	return nil
}

type mockAgentService struct {
	result       *agentpkg.AgentResult
	err          error
	calledCustom agentpkg.LLMClient
	callCount    int
}

func (m *mockAgentService) Run(
	ctx context.Context, userID int64,
	conversation []map[string]interface{},
	customLLM ...agentpkg.LLMClient,
) (*agentpkg.AgentResult, error) {
	m.callCount++
	if len(customLLM) > 0 {
		m.calledCustom = customLLM[0]
	}
	if m.err != nil {
		return nil, m.err
	}
	return m.result, nil
}

type mockLLMConfigService struct {
	cfg *userservice.FullLLMConfig
	has bool
	err error
}

func (m *mockLLMConfigService) GetFullConfigForUser(userID int64) (*userservice.FullLLMConfig, bool, error) {
	return m.cfg, m.has, m.err
}

type mockSender struct {
	sentOpenID  string
	sentContent string
	sendErr     error
	sendCount   int
	// lastMode 记录最后一次发送使用的渠道:"text" or "markdown"。
	// Agent 成功回复应走 "markdown"(飞书会渲染),fallback 兜底走 "text"。
	lastMode string
}

func (m *mockSender) SendText(ctx context.Context, openID, content string) error {
	m.sendCount++
	m.sentOpenID, m.sentContent, m.lastMode = openID, content, "text"
	return m.sendErr
}

func (m *mockSender) SendMarkdown(ctx context.Context, openID, content string) error {
	m.sendCount++
	m.sentOpenID, m.sentContent, m.lastMode = openID, content, "markdown"
	return m.sendErr
}

// ===== Helpers =====

func newHandler(
	binding BindingService, msg MessageService, agent AgentService,
	cfg LLMConfigService, sender Sender,
) *MessageHandler {
	return NewMessageHandler(binding, msg, agent, cfg, sender)
}

func okResult(records ...agentpkg.StepRecord) *agentpkg.AgentResult {
	return &agentpkg.AgentResult{FinalResponse: "好的", Records: records}
}

// ===== Tests =====

// p2p 单聊正常 pipeline:全步骤执行,steps 落库,最终文本送飞书。
func TestMessageHandler_P2PHappyPath(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	records := []agentpkg.StepRecord{
		{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, DurationMs: 10, Request: "[r]", Response: `{"content":"好的"}`},
	}
	agent := &mockAgentService{result: okResult(records...)}
	cfg := &mockLLMConfigService{has: false}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, cfg, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_xxx", Text: "你好", MsgID: "om_1", ChatType: "p2p",
	})
	require.NoError(t, err)

	// channel 落库
	// 用户消息保存(带飞书 msgID 去重)
	assert.Equal(t, "你好", msg.savedUserContent)
	assert.Equal(t, "om_1", msg.savedUserMsgID)
	// agent 被调
	assert.Equal(t, 1, agent.callCount)
	// 助手消息+steps 落库(content=纯文本投影,segments=nil)
	assert.Equal(t, 1, msg.saveAsstCalled)
	assert.Equal(t, "好的", msg.savedAsstContent)
	assert.Nil(t, msg.savedSegments)
	require.Len(t, msg.savedSteps, 1)
	assert.Equal(t, conversation.StepKindLLMCall, msg.savedSteps[0].Kind)
	assert.Equal(t, 0, msg.savedSteps[0].Seq)
	// 飞书送回最终文本 — Agent 成功回复走 markdown(让飞书渲染加粗/列表/链接)
	assert.Equal(t, 1, sender.sendCount)
	assert.Equal(t, "ou_xxx", sender.sentOpenID)
	assert.Equal(t, "好的", sender.sentContent)
	assert.Equal(t, "markdown", sender.lastMode, "Agent 成功回复应走 SendMarkdown,飞书才会渲染 markdown")
}

// 群聊消息(chat_type != p2p)忽略:不调 agent、不落库、不回复(v1.6 仅单聊)。
func TestMessageHandler_GroupChat_Ignored(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_2", ChatType: "group",
	})
	require.NoError(t, err)

	assert.Equal(t, 0, agent.callCount, "群聊不应触发 agent")
	assert.Equal(t, 0, msg.saveAsstCalled, "群聊不应落库")
	assert.Equal(t, 0, sender.sendCount, "群聊不应回复")
}

// 重复 message_id:SaveUserMessage 返回 ErrDuplicateMessage,handler 应静默丢弃(不再次回复)。
func TestMessageHandler_DuplicateMessage_Skipped(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{saveUserErr: chatsvc.ErrDuplicateMessage}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "你好", MsgID: "om_dup", ChatType: "p2p",
	})
	require.NoError(t, err, "重复消息是预期事件,不应返回 error")

	assert.Equal(t, 0, agent.callCount, "重复消息不应触发 agent")
	assert.Equal(t, 0, sender.sendCount, "重复消息不应再回复用户")
}

// 用户自定义 LLM 配置:handler 应构造 OpenAI 兼容客户端透传给 Run 的 customLLMClient。
func TestMessageHandler_CustomLLMConfig_Passthrough(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	cfg := &mockLLMConfigService{
		has: true,
		cfg: &userservice.FullLLMConfig{
			Provider: "openai", APIKey: "sk-x", BaseURL: "http://x", Model: "test-model",
		},
	}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, cfg, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_3", ChatType: "p2p",
	})
	require.NoError(t, err)

	assert.NotNil(t, agent.calledCustom, "自定义配置时应传 custom LLM 给 Run")
	// 同时 steps 的 model 字段应为 userConfig.Model
	require.Len(t, msg.savedSteps, 0, "本测试没注入 records,steps 为空 OK;model 字段断言看下个用例")
}

// 自定义 LLM 配置 + records:llm_call step 的 model 应填入 userConfig.Model。
func TestMessageHandler_CustomLLMConfig_StepModelFilled(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	records := []agentpkg.StepRecord{
		{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, Request: "[r]", Response: `{"content":"ok"}`},
		{Kind: agentpkg.StepKindToolCall, Status: agentpkg.StepStatusSuccess, Tool: "t", Request: "{}", Response: "x"},
	}
	agent := &mockAgentService{result: &agentpkg.AgentResult{FinalResponse: "答复", Records: records}}
	cfg := &mockLLMConfigService{
		has: true,
		cfg: &userservice.FullLLMConfig{
			Provider: "p", APIKey: "k", BaseURL: "http://x", Model: "custom-model-xx",
		},
	}
	h := newHandler(binding, msg, agent, cfg, &mockSender{})

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_4", ChatType: "p2p",
	})
	require.NoError(t, err)

	require.Len(t, msg.savedSteps, 2)
	assert.Equal(t, "custom-model-xx", msg.savedSteps[0].Model, "llm_call step model 应为自定义模型名")
	assert.Equal(t, "", msg.savedSteps[1].Model, "tool_call step 不填 model")
}

// agent 执行错误:handler 不崩,给飞书回兜底文案,记录用户消息但不落 assistant。
func TestMessageHandler_AgentError_FallbackReply(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{err: errors.New("upstream timeout")}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_5", ChatType: "p2p",
	})
	require.NoError(t, err, "agent 失败应被 handler 兜底,不返回 error 给 SDK 上层")

	assert.Equal(t, "hi", msg.savedUserContent, "user 消息已落库")
	assert.Equal(t, 0, msg.saveAsstCalled, "agent 失败时不落 assistant 消息")
	assert.Equal(t, 1, sender.sendCount, "应给用户回兜底文案,避免无反馈")
	assert.NotEmpty(t, sender.sentContent)
	assert.Equal(t, "text", sender.lastMode, "fallback 兜底走纯文本即可,不需要 markdown 卡片")
}

// Sender 失败:不影响 handler 主流程返回(消息已落库,只是回复发送失败),不抛 panic。
func TestMessageHandler_SenderError_DoesNotPanic(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{sendErr: errors.New("network")}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_6", ChatType: "p2p",
	})
	require.NoError(t, err, "Sender 失败仅记日志,不上抛")
	assert.Equal(t, 1, sender.sendCount)
}

// 空文本消息:跳过(无意义触发 agent)。
func TestMessageHandler_EmptyText_Skipped(t *testing.T) {
	binding := &mockBindingService{resolveUserID: 42, resolveBound: true}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "", MsgID: "om_7", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, agent.callCount)
	assert.Equal(t, 0, sender.sendCount)
}

// 编译期保证 mock 满足接口
var (
	_ BindingService  = (*mockBindingService)(nil)
	_ MessageService   = (*mockMessageService)(nil)
	_ AgentService     = (*mockAgentService)(nil)
	_ LLMConfigService = (*mockLLMConfigService)(nil)
	_ Sender           = (*mockSender)(nil)
	_                  = time.Second // 占位避免 import 漂移
)


// ===== v2.2 绑定码 / 未绑引导 测试 =====

// 绑定码格式「绑定 123456」-> 走 BindChannel,成功回复对应文案,不进对话。
func TestMessageHandler_BindCode_Success(t *testing.T) {
	binding := &mockBindingService{resolveBound: true, resolveUserID: 42} // 不会走到 resolve
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_bind", Text: "绑定 123456", MsgID: "om_b1", ChatType: "p2p",
	})
	require.NoError(t, err)

	assert.True(t, binding.bindCalled)
	assert.Equal(t, "123456", binding.bindGotCode)
	assert.Equal(t, "ou_bind", binding.bindGotOpenID)
	assert.Equal(t, 0, agent.callCount, "绑定码不进对话")
	assert.Equal(t, 0, msg.saveAsstCalled, "绑定码不落消息")
	assert.Equal(t, 1, sender.sendCount)
	assert.Equal(t, "绑定成功!现在可以在飞书跟我聊了", sender.sentContent)
	assert.Equal(t, "text", sender.lastMode)
}

// 绑定码前后有空格也应识别(TrimSpace)。
func TestMessageHandler_BindCode_TrimsSpaces(t *testing.T) {
	binding := &mockBindingService{}
	sender := &mockSender{}
	h := newHandler(binding, &mockMessageService{}, &mockAgentService{result: okResult()}, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "  绑定 654321  ", MsgID: "om_b2", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, "654321", binding.bindGotCode)
}

// 绑定码无效 -> 回 PRD 5.2 对应文案。
func TestMessageHandler_BindCode_Invalid(t *testing.T) {
	binding := &mockBindingService{bindErr: userservice.ErrCodeInvalid}
	sender := &mockSender{}
	h := newHandler(binding, &mockMessageService{}, &mockAgentService{result: okResult()}, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "绑定 999999", MsgID: "om_b3", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, "绑定码无效或已过期,请在 web 端重新获取", sender.sentContent)
}

func TestMessageHandler_BindCode_FeishuAlreadyBound(t *testing.T) {
	binding := &mockBindingService{bindErr: userservice.ErrChannelAlreadyBound}
	sender := &mockSender{}
	h := newHandler(binding, &mockMessageService{}, &mockAgentService{result: okResult()}, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "绑定 111111", MsgID: "om_b4", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, "该飞书号已绑定其他账号", sender.sentContent)
}

func TestMessageHandler_BindCode_AccountAlreadyBound(t *testing.T) {
	binding := &mockBindingService{bindErr: userservice.ErrAccountAlreadyBound}
	sender := &mockSender{}
	h := newHandler(binding, &mockMessageService{}, &mockAgentService{result: okResult()}, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "绑定 222222", MsgID: "om_b5", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, "你的账号已绑定飞书,如需更换请稍后(暂不支持)", sender.sentContent)
}

// 非绑定码格式 + 未绑定 -> 回引导,不建号、不进对话(PRD 5.4)。
func TestMessageHandler_UnboundUser_GuidedNotCreated(t *testing.T) {
	binding := &mockBindingService{resolveBound: false} // 未绑
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_nobody", Text: "你好", MsgID: "om_u1", ChatType: "p2p",
	})
	require.NoError(t, err)

	assert.False(t, binding.bindCalled, "非绑定码不应触发 BindChannel")
	assert.Equal(t, 0, agent.callCount, "未绑定不应进对话")
	assert.Equal(t, 0, msg.saveAsstCalled, "未绑定不落消息")
	assert.Equal(t, 1, sender.sendCount)
	assert.Contains(t, sender.sentContent, "你还没有绑定 OmniBot 账号")
	assert.Contains(t, sender.sentContent, "绑定 123456")
	assert.Equal(t, "text", sender.lastMode)
}

// 非绑定码格式但格式相近(如「绑定123456」无空格、「绑定 abc123」非纯数字)不应识别为绑定码,
// 走未绑引导(未绑场景)。
func TestMessageHandler_BindCode_FormatMismatch_NotTreatedAsBind(t *testing.T) {
	cases := []string{"绑定123456", "绑定 abc123", "绑定 12345", "绑定 1234567", "绑定码 123456"}
	for _, text := range cases {
		binding := &mockBindingService{resolveBound: false}
		sender := &mockSender{}
		h := newHandler(binding, &mockMessageService{}, &mockAgentService{result: okResult()}, &mockLLMConfigService{}, sender)

		err := h.HandleInbound(context.Background(), InboundMessage{
			OpenID: "ou_x", Text: text, MsgID: "om_f", ChatType: "p2p",
		})
		require.NoError(t, err)
		assert.False(t, binding.bindCalled, "格式不匹配 %q 不应触发 BindChannel", text)
	}
}

// 已绑定用户的普通消息:走对话,使用 binding 解析出的 userID(非建号)。
func TestMessageHandler_BoundUser_ChatUsesResolvedUserID(t *testing.T) {
	binding := &mockBindingService{resolveBound: true, resolveUserID: 99}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(binding, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_bound", Text: "在吗", MsgID: "om_c1", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, agent.callCount, "已绑定用户应进对话")
	assert.Equal(t, "在吗", msg.savedUserContent)
}
