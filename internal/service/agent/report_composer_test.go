package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	domainagent "omnibot/internal/domain/agent"
)

// N1:共用汇报组件——转述上下文组装 + 落库。渠道无关，飞书/微信共用。

type fakeReportRunner struct {
	result    *AgentResult
	err       error
	calls     int
	gotUserID int64
	gotConv   []map[string]interface{}
	gotLLM    LLMClient
}

func (f *fakeReportRunner) Run(_ context.Context, userID int64, conv []map[string]interface{}, custom ...LLMClient) (*AgentResult, error) {
	f.calls++
	f.gotUserID = userID
	f.gotConv = conv
	if len(custom) > 0 {
		f.gotLLM = custom[0]
	}
	return f.result, f.err
}

type fakeReportCtxSource struct {
	msgs []llm.ChatMessage
	err  error
}

func (f *fakeReportCtxSource) BuildContextMessages(context.Context, int64, string) ([]llm.ChatMessage, error) {
	return f.msgs, f.err
}

type fakeReportStore struct {
	called  bool
	userID  int64
	taskID  int64
	turnID  int64
	content string
	steps   []*conversation.AgentStep
	err     error
}

func (f *fakeReportStore) SaveReportMessage(_ context.Context, userID, taskID, turnID int64, content string, _ []conversation.MessageSegment, steps []*conversation.AgentStep) error {
	f.called = true
	f.userID = userID
	f.taskID = taskID
	f.turnID = turnID
	f.content = content
	f.steps = steps
	return f.err
}

type fakeReportLLM struct {
	apiKey, baseURL, model string
	has                    bool
	err                    error
}

func (f *fakeReportLLM) GetFullConfig(int64) (string, string, string, bool, error) {
	return f.apiKey, f.baseURL, f.model, f.has, f.err
}

func stubTaskForComposer(source string) *domainagent.AgentTask {
	t := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("查询北京天气"), source, "target-1")
	art := "北京未来7天晴"
	t.Artifact = &art
	t.Status = domainagent.TaskStatusCompleted
	t.ID = 99
	return t
}

func TestReportComposer_ComposeBuildsAnchoredContext(t *testing.T) {
	runner := &fakeReportRunner{result: &AgentResult{FinalResponse: "主人，北京七天都晴"}}
	// 真实实现会把「虚拟触发」作为最后一条拼进历史,这里对齐该行为
	ctxSource := &fakeReportCtxSource{msgs: []llm.ChatMessage{
		{Role: "user", Content: "帮我查下北京天气"},
		{Role: "assistant", Content: "已安排后台任务"},
		{Role: "user", Content: "请汇报这个子任务的结果。"},
	}}

	c := NewReportComposer(runner, ctxSource, &fakeReportStore{}, nil)
	content, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))

	require.True(t, ok)
	assert.Equal(t, "主人，北京七天都晴", content)

	conv := runner.gotConv
	require.GreaterOrEqual(t, len(conv), 4, "system + 2 条历史 + 虚拟触发")
	assert.Equal(t, "system", conv[0]["role"])
	instruction, _ := conv[0]["content"].(string)
	assert.Contains(t, instruction, "子任务完成回执")

	joined := ""
	for _, m := range conv {
		if s, _ := m["content"].(string); s != "" {
			joined += s
		}
	}
	assert.Contains(t, joined, "帮我查下北京天气", "必须锚定原始对话")

	last := conv[len(conv)-1]
	assert.Equal(t, "user", last["role"])
	lastContent, _ := last["content"].(string)
	assert.Contains(t, lastContent, "请汇报这个子任务的结果")
}

func TestReportComposer_ComposeFailsOnRunnerError(t *testing.T) {
	runner := &fakeReportRunner{err: assert.AnError}
	c := NewReportComposer(runner, &fakeReportCtxSource{}, &fakeReportStore{}, nil)

	_, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))
	assert.False(t, ok, "转述失败应交给渠道回落裸回执")
}

func TestReportComposer_ComposeFailsOnEmptyResponse(t *testing.T) {
	runner := &fakeReportRunner{result: &AgentResult{FinalResponse: "   "}}
	c := NewReportComposer(runner, &fakeReportCtxSource{}, &fakeReportStore{}, nil)

	_, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))
	assert.False(t, ok)
}

// 对话历史拉取失败 → 降级为「只发回执」，不阻断转述。
func TestReportComposer_ComposeDegradesWhenHistoryFails(t *testing.T) {
	runner := &fakeReportRunner{result: &AgentResult{FinalResponse: "汇报"}}
	ctxSource := &fakeReportCtxSource{err: assert.AnError}
	c := NewReportComposer(runner, ctxSource, &fakeReportStore{}, nil)

	_, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))
	require.True(t, ok)
	assert.Len(t, runner.gotConv, 2, "system + 虚拟触发")
}

func TestReportComposer_SaveUsesOriginTurnAndSteps(t *testing.T) {
	store := &fakeReportStore{}
	task := stubTaskForComposer(domainagent.SourceWeChatClaw)
	turnID := int64(7)
	task.OriginTurnID = &turnID

	c := NewReportComposer(&fakeReportRunner{}, &fakeReportCtxSource{}, store, nil)
	err := c.Save(context.Background(), task, "汇报正文", []StepRecord{{Kind: StepKindLLMCall, Status: "success"}})
	require.NoError(t, err)

	assert.True(t, store.called)
	assert.Equal(t, int64(42), store.userID)
	assert.Equal(t, int64(99), store.taskID)
	assert.Equal(t, int64(7), store.turnID, "report 逻辑归属原始 Turn")
	assert.Equal(t, "汇报正文", store.content)
	assert.Len(t, store.steps, 1)
}

func TestReportComposer_SaveNoOriginTurn(t *testing.T) {
	store := &fakeReportStore{}
	task := stubTaskForComposer(domainagent.SourceFeishu)
	task.OriginTurnID = nil

	c := NewReportComposer(&fakeReportRunner{}, &fakeReportCtxSource{}, store, nil)
	require.NoError(t, c.Save(context.Background(), task, "x", nil))
	assert.Equal(t, int64(0), store.turnID)
}

// 用户配了自定义 LLM → 转述要用它（与主对话一致）。
func TestReportComposer_UsesUserLLMConfig(t *testing.T) {
	runner := &fakeReportRunner{result: &AgentResult{FinalResponse: "ok"}}
	llmCfg := &fakeReportLLM{apiKey: "sk-x", baseURL: "https://proxy", model: "m", has: true}

	c := NewReportComposer(runner, &fakeReportCtxSource{}, &fakeReportStore{}, llmCfg)
	_, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))

	require.True(t, ok)
	assert.NotNil(t, runner.gotLLM, "应传入按用户配置构造的 client")
}

func TestReportComposer_NoUserConfigUsesDefaultLLM(t *testing.T) {
	runner := &fakeReportRunner{result: &AgentResult{FinalResponse: "ok"}}
	c := NewReportComposer(runner, &fakeReportCtxSource{}, &fakeReportStore{}, &fakeReportLLM{has: false})

	_, _, ok := c.Compose(context.Background(), stubTaskForComposer(domainagent.SourceFeishu))
	require.True(t, ok)
	assert.Nil(t, runner.gotLLM, "未配置时不传 client，由 Agent 用系统默认")
}
