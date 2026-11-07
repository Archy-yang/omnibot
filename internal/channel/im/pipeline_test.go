package im

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	agentpkg "omnibot/internal/service/agent"
	chatsvc "omnibot/internal/service/chat"
	userservice "omnibot/internal/service/user"
)

// ---------- fakes:完整 pipeline ----------

type fakeMessages struct {
	turnID       int64
	saveUserErr  error
	savedUser    string
	savedMsgID   string
	saveAsst     string
	saveAsstCall int
	buildErr     error
	buildCtx     []llm.ChatMessage
}

func (f *fakeMessages) SaveUserMessage(ctx context.Context, userID int64, content, msgID string) (int64, error) {
	f.savedUser, f.savedMsgID = content, msgID
	return f.turnID, f.saveUserErr
}

func (f *fakeMessages) BuildContextMessages(ctx context.Context, userID int64, current string) ([]llm.ChatMessage, error) {
	if f.buildErr != nil {
		return nil, f.buildErr
	}
	if f.buildCtx != nil {
		return f.buildCtx, nil
	}
	return []llm.ChatMessage{{Role: "user", Content: current}}, nil
}

func (f *fakeMessages) SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error {
	f.saveAsstCall++
	f.saveAsst = content
	return nil
}

type fakeAgent struct {
	result  *agentpkg.AgentResult
	err     error
	calls   int
	gotCtx  context.Context
	gotConv []map[string]interface{}
}

func (f *fakeAgent) Run(ctx context.Context, userID int64, conv []map[string]interface{}, customLLMClient ...agentpkg.LLMClient) (*agentpkg.AgentResult, error) {
	f.calls++
	f.gotCtx = ctx
	f.gotConv = conv
	return f.result, f.err
}

type fakeLLMConfig struct{}

func (fakeLLMConfig) GetFullConfigForUser(userID int64) (*userservice.FullLLMConfig, bool, error) {
	return nil, false, nil
}

type fakeNoteFeedback struct {
	reply   string
	err     error
	gotUser int64
	gotFB   string
}

func (f *fakeNoteFeedback) FeedbackLatestDeliveredNote(userID int64, feedback string) (string, error) {
	f.gotUser, f.gotFB = userID, feedback
	return f.reply, f.err
}

type fakeMemorySvc struct {
	handled string
}

func (f *fakeMemorySvc) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	return &memorydomain.Memory{Content: content}, nil
}

func (f *fakeMemorySvc) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return nil, nil
}

func (f *fakeMemorySvc) Clear(ctx context.Context, userID int64) error { return nil }

func (f *fakeMemorySvc) Delete(ctx context.Context, userID int64, memoryID int64) (bool, error) {
	return true, nil
}

func newPipelineHandler(binding BindingService, sender Sender, msgs MessageService, ag AgentService) *Handler {
	return NewHandler(Options{
		ChannelType: "wechat_claw",
		Source:      "wechat_claw",
		Binding:     binding,
		Messages:    msgs,
		Agent:       ag,
		LLMConfig:   fakeLLMConfig{},
		Sender:      sender,
	})
}

func TestHandleInboundBoundHappyPath(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	msgs := &fakeMessages{turnID: 11}
	ag := &fakeAgent{result: &agentpkg.AgentResult{FinalResponse: "好的,已经安排"}}
	h := newPipelineHandler(binding, sender, msgs, ag)

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "帮我看看明天的安排", MsgID: "m-1", ChatType: "p2p",
	})
	require.NoError(t, err)

	// 落库 + agent + 回复三段都走通
	assert.Equal(t, "帮我看看明天的安排", msgs.savedUser)
	assert.Equal(t, "m-1", msgs.savedMsgID)
	assert.Equal(t, 1, ag.calls)
	assert.Equal(t, "好的,已经安排", msgs.saveAsst)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "好的,已经安排", sender.sent[0])
}

func TestHandleInboundDuplicateMessageDropped(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	msgs := &fakeMessages{saveUserErr: chatsvc.ErrDuplicateMessage}
	ag := &fakeAgent{result: &agentpkg.AgentResult{FinalResponse: "x"}}
	h := newPipelineHandler(binding, sender, msgs, ag)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "重复投递", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 0, ag.calls, "重复消息不应再跑 agent")
	assert.Empty(t, sender.sent)
}

func TestHandleInboundAgentFailureSendsFallback(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	msgs := &fakeMessages{turnID: 1}
	ag := &fakeAgent{err: errors.New("agent boom")}
	h := newPipelineHandler(binding, sender, msgs, ag)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "随便问问", MsgID: "m-1", ChatType: "p2p",
	}))

	require.Len(t, sender.sent, 1)
	assert.Equal(t, fallbackReplyAgent, sender.sent[0])
	assert.Equal(t, 0, msgs.saveAsstCall, "失败不落 assistant 消息")
}

func TestHandleInboundNoteFeedbackShortCircuits(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	ag := &fakeAgent{result: &agentpkg.AgentResult{FinalResponse: "x"}}
	svc := &fakeNoteFeedback{reply: "收到,以后不再提这件事。"}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, ag)
	h.SetNoteFeedbackService(svc)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "不用再提", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 0, ag.calls)
	assert.Equal(t, int64(7), svc.gotUser)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "收到,以后不再提这件事。", sender.sent[0])
}

func TestHandleInboundNoteFeedbackErrorStillShortCircuits(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	ag := &fakeAgent{}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, ag)
	h.SetNoteFeedbackService(&fakeNoteFeedback{err: errors.New("no note")})

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "有用", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 0, ag.calls)
	require.Len(t, sender.sent, 1)
	assert.Equal(t, "最近没有可反馈的主动消息。", sender.sent[0])
}

func TestHandleInboundMemoryCommand(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	ag := &fakeAgent{}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, ag)
	h.SetMemoryService(&fakeMemorySvc{})

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "#记住 我偏好简洁回答", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 0, ag.calls)
	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0], "已记住")
}

func TestHandleInboundResolveError(t *testing.T) {
	binding := &fakeBinding{resolveErr: errors.New("db down")}
	h := newBindHandler(binding, &fakeSender{})

	err := h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "你好", MsgID: "m-1", ChatType: "p2p",
	})
	require.Error(t, err, "解析失败应上抛让上层重试/告警")
}

func TestToAgentMessagesAndRecordsToAgentSteps(t *testing.T) {
	msgs := ToAgentMessages([]llm.ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "yo", ToolCalls: []map[string]interface{}{{"id": "t1"}}, ToolCallID: "t1"},
	})
	require.Len(t, msgs, 2)
	assert.Equal(t, "user", msgs[0]["role"])
	assert.Equal(t, "hi", msgs[0]["content"])
	_, hasToolCalls := msgs[1]["tool_calls"]
	assert.True(t, hasToolCalls)
	assert.Equal(t, "t1", msgs[1]["tool_call_id"])

	steps := RecordsToAgentSteps(nil, 1, "model-x")
	assert.Empty(t, steps)
}

func TestHandleInboundBuildContextErrorFallsBack(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	msgs := &fakeMessages{buildErr: errors.New("ctx boom")}
	ag := &fakeAgent{result: &agentpkg.AgentResult{FinalResponse: "ok"}}
	h := newPipelineHandler(binding, sender, msgs, ag)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "你好", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 1, ag.calls, "上下文构建失败也要继续跑 agent")
	require.NotEmpty(t, ag.gotConv)
	assert.Equal(t, "你好", ag.gotConv[0]["content"])
}

type failingMemory struct{}

func (failingMemory) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	return nil, errors.New("remember boom")
}
func (failingMemory) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return nil, errors.New("list boom")
}
func (failingMemory) Clear(ctx context.Context, userID int64) error { return errors.New("clear boom") }
func (failingMemory) Delete(ctx context.Context, userID int64, memoryID int64) (bool, error) {
	return false, errors.New("delete boom")
}

func TestMemoryCommandErrorsReturnFallback(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, &fakeAgent{})
	h.SetMemoryService(failingMemory{})

	for _, text := range []string{"#记住 喜欢咖啡", "#我的记忆", "#清空记忆"} {
		sender.sent = nil
		require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
			OpenID: "peer-1", Text: text, MsgID: "m-1", ChatType: "p2p",
		}))
		require.Len(t, sender.sent, 1, text)
		assert.Equal(t, fallbackReplyAgent, sender.sent[0], text)
	}
}

type fakeReporter struct {
	instruction string
	ids         []int64
	reported    []int64
}

func (f *fakeReporter) GetPendingReportContext(userID int64, source string) (string, []int64) {
	return f.instruction, f.ids
}

func (f *fakeReporter) MarkReported(taskID int64) error {
	f.reported = append(f.reported, taskID)
	return nil
}

func TestSubAgentReporterInjectsInstructionAndMarksReported(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	ag := &fakeAgent{result: &agentpkg.AgentResult{FinalResponse: "汇报完成"}}
	reporter := &fakeReporter{instruction: "先汇报后台任务", ids: []int64{5, 6}}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, ag)
	h.SetSubAgentReporter(reporter)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "在吗", MsgID: "m-1", ChatType: "p2p",
	}))

	require.NotEmpty(t, ag.gotConv)
	assert.Equal(t, "system", ag.gotConv[0]["role"])
	assert.Equal(t, "先汇报后台任务", ag.gotConv[0]["content"])
	assert.Equal(t, []int64{5, 6}, reporter.reported, "汇报后应标记防重复")
}

type fakeLarkAuth struct {
	url   string
	code  string
	err   error
	calls int
}

func (f *fakeLarkAuth) Relogin(ctx context.Context, userID int64) (string, string, error) {
	f.calls++
	return f.url, f.code, f.err
}

// 19 号补强:IM 里发 #飞书授权 → 回验证链接,不进对话。
func TestHandleInboundLarkAuthCommand(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	ag := &fakeAgent{}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, ag)
	auth := &fakeLarkAuth{url: "https://accounts.feishu.cn/verify?x=1", code: "CODE-9"}
	h.SetLarkAuthCommander(auth)

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "#飞书授权", MsgID: "m-1", ChatType: "p2p",
	}))

	assert.Equal(t, 1, auth.calls)
	assert.Equal(t, 0, ag.calls, "授权命令不该进对话")
	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0], "https://accounts.feishu.cn/verify?x=1")
	assert.Contains(t, sender.sent[0], "CODE-9")
}

func TestHandleInboundLarkAuthCommandFailure(t *testing.T) {
	binding := &fakeBinding{resolveBound: true, resolveUserID: 7}
	sender := &fakeSender{}
	h := newPipelineHandler(binding, sender, &fakeMessages{}, &fakeAgent{})
	h.SetLarkAuthCommander(&fakeLarkAuth{err: errors.New("no record")})

	require.NoError(t, h.HandleInbound(context.Background(), InboundMessage{
		OpenID: "peer-1", Text: "#飞书授权", MsgID: "m-1", ChatType: "p2p",
	}))

	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0], "失败")
}
