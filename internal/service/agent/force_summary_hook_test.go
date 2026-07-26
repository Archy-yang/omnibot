package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestForceSummaryHook_OnMaxExhausted 达 MaxSteps 时强制做无工具 LLM 调用,返回汇总文本。
// 验证:传空 tools、追加汇总提示、emit token 实时、返回内容。
func TestForceSummaryHook_OnMaxExhausted(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{{ContentDelta: "汇总报告:..."}, {FinishReason: "stop"}, {Done: true}},
		},
	}
	h := NewForceSummaryHook(llm)

	var emitted []AgentEvent
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rt := &Runtime{
		Ctx:      ctx,
		Emit:     func(e AgentEvent) { emitted = append(emitted, e) },
		Messages: []map[string]interface{}{{"role": "system", "content": "sys"}, {"role": "user", "content": "u"}},
	}

	got := h.OnMaxExhausted(rt)

	require.NotEmpty(t, got)
	assert.Contains(t, got, "汇总报告")
	// 应 emit token(实时)+ llm_call
	assert.True(t, len(emitted) >= 2, "应 emit token 和 llm_call")
	hasToken := false
	hasLLMCall := false
	for _, e := range emitted {
		if e.Type == AgentEventToken {
			hasToken = true
		}
		if e.Type == AgentEventLLMCall {
			hasLLMCall = true
			assert.Equal(t, StepStatusSuccess, e.StepStatus)
		}
	}
	assert.True(t, hasToken, "应实时 emit token")
	assert.True(t, hasLLMCall, "应记 llm_call 步骤")
}

// TestForceSummaryHook_PassesEmptyTools 汇总调用必须传空 tools(强制只产出文本,不能再调工具)。
func TestForceSummaryHook_PassesEmptyTools(t *testing.T) {
	capture := &captureToolsLLMClient{mockStreamingLLMClient: &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{{{ContentDelta: "x"}, {Done: true}}},
	}}
	h := NewForceSummaryHook(capture)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rt := &Runtime{
		Ctx:      ctx,
		Emit:     func(AgentEvent) {},
		Messages: []map[string]interface{}{{"role": "user", "content": "u"}},
	}

	h.OnMaxExhausted(rt)

	require.Len(t, capture.toolsPerCall, 1, "应调一次 LLM")
	assert.Empty(t, capture.toolsPerCall[0], "汇总调用 tools 必须为空")
}

// TestForceSummaryHook_LLMErrorReturnsEmpty LLM 调用失败返回空(交循环回落兜底文案)。
func TestForceSummaryHook_LLMErrorReturnsEmpty(t *testing.T) {
	llm := &mockStreamingLLMClient{openErr: newSimpleErr("503")}
	h := NewForceSummaryHook(llm)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rt := &Runtime{Ctx: ctx, Emit: func(AgentEvent) {}, Messages: []map[string]interface{}{{"role": "user", "content": "u"}}}

	got := h.OnMaxExhausted(rt)
	assert.Empty(t, got, "LLM 失败应返回空")
}

// TestForceSummaryHook_AppendsSummaryInstruction 汇总调用前应追加 user 提示要求立即产出报告。
func TestForceSummaryHook_AppendsSummaryInstruction(t *testing.T) {
	capture := &captureMsgsLLMClient{mockStreamingLLMClient: &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{{{ContentDelta: "x"}, {Done: true}}},
	}}
	h := NewForceSummaryHook(capture)

	baseMsgs := []map[string]interface{}{{"role": "user", "content": "原始"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rt := &Runtime{Ctx: ctx, Emit: func(AgentEvent) {}, Messages: baseMsgs}

	h.OnMaxExhausted(rt)

	require.Len(t, capture.msgsPerCall, 1)
	lastMsgs := capture.msgsPerCall[0]
	// 最后一条应是追加的汇总提示
	require.GreaterOrEqual(t, len(lastMsgs), 2)
	last := lastMsgs[len(lastMsgs)-1]
	assert.Equal(t, "user", last["role"])
	assert.Contains(t, last["content"].(string), "立即")
	// 不应修改原 Messages
	assert.Len(t, baseMsgs, 1, "不应修改原 messages")
}

// captureMsgsLLMClient 记录每次调用传入的 messages(深拷贝快照)。
type captureMsgsLLMClient struct {
	*mockStreamingLLMClient
	msgsPerCall [][]map[string]interface{}
}

func (c *captureMsgsLLMClient) ChatCompletionStream(
	ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	snap := make([]map[string]interface{}, len(messages))
	copy(snap, messages)
	c.msgsPerCall = append(c.msgsPerCall, snap)
	return c.mockStreamingLLMClient.ChatCompletionStream(ctx, messages, tools)
}
