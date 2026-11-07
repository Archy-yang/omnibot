package wechatclaw

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	agentpkg "omnibot/internal/service/agent"
)

// N2:微信 ClawBot 任务完成推送。纯文本(SendText),sender 动态获取。

type fakeSender struct {
	sent    []string
	targets []string
	err     error
}

func (f *fakeSender) SendText(_ context.Context, openID, content string) error {
	f.targets = append(f.targets, openID)
	if f.err != nil {
		return f.err
	}
	f.sent = append(f.sent, content)
	return nil
}

type fakeRunner struct {
	result *agentpkg.AgentResult
	err    error
}

func (f *fakeRunner) Run(context.Context, int64, []map[string]interface{}, ...agentpkg.LLMClient) (*agentpkg.AgentResult, error) {
	return f.result, f.err
}

type fakeCtxSource struct{}

func (fakeCtxSource) BuildContextMessages(context.Context, int64, string) ([]llm.ChatMessage, error) {
	return []llm.ChatMessage{{Role: "user", Content: "帮我查下"}}, nil
}

type fakeStore struct {
	called  bool
	taskID  int64
	content string
}

func (f *fakeStore) SaveReportMessage(_ context.Context, _, taskID, _ int64, content string, _ []conversation.MessageSegment, _ []*conversation.AgentStep) error {
	f.called = true
	f.taskID = taskID
	f.content = content
	return nil
}

func newStubTask() *domainagent.AgentTask {
	t := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("查天气"), domainagent.SourceWeChatClaw, "wx-user-1")
	art := "北京晴,25-32 度"
	t.Artifact = &art
	t.Status = domainagent.TaskStatusCompleted
	t.ID = 88
	return t
}

func TestTaskNotifier_SendsTextAndSaves(t *testing.T) {
	sender := &fakeSender{}
	store := &fakeStore{}
	composer := agentpkg.NewReportComposer(
		&fakeRunner{result: &agentpkg.AgentResult{FinalResponse: "主人,北京这周都晴"}},
		fakeCtxSource{}, store, nil,
	)
	n := NewTaskNotifier(func() TextSender { return sender }, composer)

	require.NoError(t, n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask()))

	require.Len(t, sender.sent, 1)
	assert.Equal(t, "wx-user-1", sender.targets[0])
	assert.Contains(t, sender.sent[0], "子任务完成汇报", "微信侧要有标题")
	assert.Contains(t, sender.sent[0], "主人,北京这周都晴")
	assert.NotContains(t, sender.sent[0], "[子任务完成回执]", "转述成功不应是裸回执")
	assert.True(t, store.called, "应落 report 消息")
	assert.Equal(t, int64(88), store.taskID)
}

func TestTaskNotifier_FallsBackToReceipt(t *testing.T) {
	sender := &fakeSender{}
	store := &fakeStore{}
	composer := agentpkg.NewReportComposer(&fakeRunner{err: errors.New("llm down")}, fakeCtxSource{}, store, nil)
	n := NewTaskNotifier(func() TextSender { return sender }, composer)

	require.NoError(t, n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask()))

	require.Len(t, sender.sent, 1)
	assert.Contains(t, sender.sent[0], "[子任务完成回执]", "转述失败必须回落裸回执")
	assert.False(t, store.called, "回落不落库")
}

// 未连接(重扫中/断线)时返回错误,由上层记日志;不能 panic。
func TestTaskNotifier_NotConnected(t *testing.T) {
	n := NewTaskNotifier(func() TextSender { return nil }, nil)

	err := n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not connected")
}

// sender 必须每次动态解析:重扫后换新 sender,旧实例不能被缓存。
func TestTaskNotifier_ResolvesSenderEachCall(t *testing.T) {
	var current TextSender
	n := NewTaskNotifier(func() TextSender { return current }, agentpkg.NewReportComposer(
		&fakeRunner{result: &agentpkg.AgentResult{FinalResponse: "汇报"}}, fakeCtxSource{}, &fakeStore{}, nil,
	))

	first := &fakeSender{}
	current = first
	require.NoError(t, n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask()))

	second := &fakeSender{}
	current = second
	require.NoError(t, n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask()))

	assert.Len(t, first.sent, 1, "第一次走旧 sender")
	assert.Len(t, second.sent, 1, "重连后应走新 sender")
}

func TestTaskNotifier_SenderErrorPropagates(t *testing.T) {
	sender := &fakeSender{err: errors.New("send failed")}
	n := NewTaskNotifier(func() TextSender { return sender }, agentpkg.NewReportComposer(
		&fakeRunner{result: &agentpkg.AgentResult{FinalResponse: "汇报"}}, fakeCtxSource{}, &fakeStore{}, nil,
	))

	err := n.NotifyTaskCompleted(context.Background(), "wx-user-1", newStubTask())
	require.Error(t, err, "发送失败要上抛,由 SubAgentService 记日志")
}

// SubAgentService 依赖 Available() 判断是否先标记 reported(断线时保留兜底)。
func TestTaskNotifier_Available(t *testing.T) {
	assert.False(t, NewTaskNotifier(func() TextSender { return nil }, nil).Available())

	// 显式声明为接口类型:避免 Go 的 typed-nil(把 nil *fakeSender 装进接口后 != nil)
	var current TextSender = &fakeSender{}
	n := NewTaskNotifier(func() TextSender { return current }, nil)
	assert.True(t, n.Available())

	current = nil
	assert.False(t, n.Available(), "断开后应变为不可用")
}
