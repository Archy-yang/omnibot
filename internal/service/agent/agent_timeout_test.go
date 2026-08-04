package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReActAgent_RunStream_TimeoutForcesSummary P0:ctx 超时(在两轮之间)应走 OnMaxExhausted
// 强制产出基于已收集信息的报告,而非吐"处理超时"废话。
//
// 场景:第一轮正常调工具;第二轮 LLM 流阻塞(慢响应),ctx 超时后 range 退出->
// 第三轮循环开头 select 命中 ctx.Done -> 走 OnMaxExhausted。
// 断言 Final=hook 汇总文本(非"处理超时"固定废话)。
func TestReActAgent_RunStream_TimeoutForcesSummary(t *testing.T) {
	// customStreamLLM:第一轮返回工具调用,第二轮阻塞到 ctx.Done(模拟慢响应超时)。
	llm := &customStreamLLM{
		round1: []LLMStreamChunk{
			{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "call_1", Name: "slow_tool"}},
			{ToolCallDelta: &ToolCallDelta{Index: 0, ArgumentsDelta: "{}"}},
			{FinishReason: "tool_calls"},
			{Done: true},
		},
		blockSecond: true,
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:        "slow_tool",
		Description: "Slow tool that blocks until ctx timeout",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			// 阻塞到 ctx 超时,使下一轮循环开头 select 命中 ctx.Done
			<-ctx.Done()
			return "慢工具结果", nil
		},
	})
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            80 * time.Millisecond, // 工具阻塞到 ctx 超时,下一轮开头 ctx.Done
		Hooks:              []RoundHook{&stubSummaryHook{summary: "基于已有信息的汇总报告"}},
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "查一下"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	var finalContent string
	for _, e := range events {
		if e.Type == AgentEventFinal {
			finalContent = e.Content
		}
	}
	require.NotEmpty(t, finalContent, "应有 Final 事件")
	assert.Equal(t, "基于已有信息的汇总报告", finalContent, "超时应走 OnMaxExhausted 产出汇总,非固定废话")
	assert.NotContains(t, finalContent, "处理超时", "不应是固定超时废话文案")
}

// customStreamLLM 第一次返回预设 chunks,第二次阻塞到 ctx.Done(模拟慢响应超时)。
type customStreamLLM struct {
	round1      []LLMStreamChunk
	blockSecond bool
	callCount   int
}

func (m *customStreamLLM) ChatCompletionStream(
	ctx context.Context,
	_ []map[string]interface{},
	_ []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	m.callCount++
	if m.callCount == 1 {
		ch := make(chan LLMStreamChunk, len(m.round1))
		for _, c := range m.round1 {
			ch <- c
		}
		close(ch)
		return ch, nil
	}
	// 第二次:阻塞到 ctx.Done 再 close(模拟慢响应)
	ch := make(chan LLMStreamChunk)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// stubSummaryHook 测试用 hook:OnMaxExhausted 返回固定汇总文本,其余空实现。
type stubSummaryHook struct{ summary string }

func (h *stubSummaryHook) BeforeRound(rt *Runtime) []map[string]interface{} { return rt.Tools }
func (h *stubSummaryHook) OnLLMResult(rt *Runtime, _, _ string, _ []ToolCall) bool {
	return true
}
func (h *stubSummaryHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	return "", "", false
}
func (h *stubSummaryHook) OnMaxExhausted(rt *Runtime) string { return h.summary }
