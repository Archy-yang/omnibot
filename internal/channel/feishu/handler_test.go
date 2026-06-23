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
	domainuser "omnibot/internal/domain/user"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	userservice "omnibot/internal/service/user"
)

// ===== Mocks =====

type mockUserService struct {
	gotChannelType string
	gotOpenID      string
	user           *domainuser.User
	err            error
}

func (m *mockUserService) GetOrCreateByChannel(ct, cid string) (*domainuser.User, *domainuser.UserChannel, bool, error) {
	m.gotChannelType, m.gotOpenID = ct, cid
	return m.user, nil, false, m.err
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
}

func (m *mockSender) SendText(ctx context.Context, openID, content string) error {
	m.sendCount++
	m.sentOpenID, m.sentContent = openID, content
	return m.sendErr
}

// ===== Helpers =====

func newHandler(
	user UserService, msg MessageService, agent AgentService,
	cfg LLMConfigService, sender Sender,
) *MessageHandler {
	return NewMessageHandler(user, msg, agent, cfg, sender)
}

func okResult(records ...agentpkg.StepRecord) *agentpkg.AgentResult {
	return &agentpkg.AgentResult{FinalResponse: "好的", Records: records}
}

// ===== Tests =====

// p2p 单聊正常 pipeline:全步骤执行,steps 落库,最终文本送飞书。
func TestMessageHandler_P2PHappyPath(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	records := []agentpkg.StepRecord{
		{Kind: agentpkg.StepKindLLMCall, Status: agentpkg.StepStatusSuccess, DurationMs: 10, Request: "[r]", Response: `{"content":"好的"}`},
	}
	agent := &mockAgentService{result: okResult(records...)}
	cfg := &mockLLMConfigService{has: false}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, cfg, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_xxx", Text: "你好", MsgID: "om_1", ChatType: "p2p",
	})
	require.NoError(t, err)

	// channel 落库
	assert.Equal(t, "feishu", user.gotChannelType)
	assert.Equal(t, "ou_xxx", user.gotOpenID)
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
	// 飞书送回最终文本
	assert.Equal(t, 1, sender.sendCount)
	assert.Equal(t, "ou_xxx", sender.sentOpenID)
	assert.Equal(t, "好的", sender.sentContent)
}

// 群聊消息(chat_type != p2p)忽略:不调 agent、不落库、不回复(v1.6 仅单聊)。
func TestMessageHandler_GroupChat_Ignored(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_2", ChatType: "group",
	})
	require.NoError(t, err)

	assert.Equal(t, 0, agent.callCount, "群聊不应触发 agent")
	assert.Equal(t, 0, msg.saveAsstCalled, "群聊不应落库")
	assert.Equal(t, 0, sender.sendCount, "群聊不应回复")
	assert.Equal(t, "", user.gotChannelType, "群聊不应创建用户")
}

// 重复 message_id:SaveUserMessage 返回 ErrDuplicateMessage,handler 应静默丢弃(不再次回复)。
func TestMessageHandler_DuplicateMessage_Skipped(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{saveUserErr: chatsvc.ErrDuplicateMessage}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "你好", MsgID: "om_dup", ChatType: "p2p",
	})
	require.NoError(t, err, "重复消息是预期事件,不应返回 error")

	assert.Equal(t, 0, agent.callCount, "重复消息不应触发 agent")
	assert.Equal(t, 0, sender.sendCount, "重复消息不应再回复用户")
}

// 用户自定义 LLM 配置:handler 应构造 OpenAI 兼容客户端透传给 Run 的 customLLMClient。
func TestMessageHandler_CustomLLMConfig_Passthrough(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	cfg := &mockLLMConfigService{
		has: true,
		cfg: &userservice.FullLLMConfig{
			Provider: "openai", APIKey: "sk-x", BaseURL: "http://x", Model: "test-model",
		},
	}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, cfg, sender)

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
	user := &mockUserService{user: &domainuser.User{ID: 42}}
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
	h := newHandler(user, msg, agent, cfg, &mockSender{})

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
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	agent := &mockAgentService{err: errors.New("upstream timeout")}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_5", ChatType: "p2p",
	})
	require.NoError(t, err, "agent 失败应被 handler 兜底,不返回 error 给 SDK 上层")

	assert.Equal(t, "hi", msg.savedUserContent, "user 消息已落库")
	assert.Equal(t, 0, msg.saveAsstCalled, "agent 失败时不落 assistant 消息")
	assert.Equal(t, 1, sender.sendCount, "应给用户回兜底文案,避免无反馈")
	assert.NotEmpty(t, sender.sentContent)
}

// Sender 失败:不影响 handler 主流程返回(消息已落库,只是回复发送失败),不抛 panic。
func TestMessageHandler_SenderError_DoesNotPanic(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{sendErr: errors.New("network")}
	h := newHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "hi", MsgID: "om_6", ChatType: "p2p",
	})
	require.NoError(t, err, "Sender 失败仅记日志,不上抛")
	assert.Equal(t, 1, sender.sendCount)
}

// 空文本消息:跳过(无意义触发 agent)。
func TestMessageHandler_EmptyText_Skipped(t *testing.T) {
	user := &mockUserService{user: &domainuser.User{ID: 42}}
	msg := &mockMessageService{}
	agent := &mockAgentService{result: okResult()}
	sender := &mockSender{}
	h := newHandler(user, msg, agent, &mockLLMConfigService{}, sender)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "ou_x", Text: "", MsgID: "om_7", ChatType: "p2p",
	})
	require.NoError(t, err)
	assert.Equal(t, 0, agent.callCount)
	assert.Equal(t, 0, sender.sendCount)
}

// 编译期保证 mock 满足接口
var (
	_ UserService      = (*mockUserService)(nil)
	_ MessageService   = (*mockMessageService)(nil)
	_ AgentService     = (*mockAgentService)(nil)
	_ LLMConfigService = (*mockLLMConfigService)(nil)
	_ Sender           = (*mockSender)(nil)
	_                  = time.Second // 占位避免 import 漂移
)
