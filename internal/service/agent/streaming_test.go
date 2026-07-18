package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockStreamingLLMClient 模拟流式 LLM 客户端。每次 ChatCompletionStream 调用对应一轮 ReAct，
// 按预先脚本好的 chunks 推到一个 channel，然后关闭 channel。多轮场景由调用方调多次。
type mockStreamingLLMClient struct {
	rounds    [][]LLMStreamChunk // 每轮一个 chunks 序列
	callCount int
	openErr   error // 用于模拟 stream 打开失败
}

func (m *mockStreamingLLMClient) ChatCompletionStream(
	ctx context.Context,
	messages []map[string]interface{},
	tools []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	if m.openErr != nil {
		return nil, m.openErr
	}
	if m.callCount >= len(m.rounds) {
		// 安全兜底：超出预设轮数返回空流（让 agent 走 fallback）
		ch := make(chan LLMStreamChunk)
		close(ch)
		m.callCount++
		return ch, nil
	}
	chunks := m.rounds[m.callCount]
	m.callCount++

	ch := make(chan LLMStreamChunk, len(chunks))
	for _, c := range chunks {
		ch <- c
	}
	close(ch)
	return ch, nil
}

// drainEvents 从 AgentEvent channel 收集所有事件直到关闭，便于断言。
func drainEvents(t *testing.T, ch <-chan AgentEvent) []AgentEvent {
	t.Helper()
	var events []AgentEvent
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, e)
		case <-timeout:
			t.Fatal("timed out waiting for AgentEvent channel close")
			return events
		}
	}
}

// TestReActAgent_RunStream_NoToolCall：LLM 直接给文本回答（无工具调用），
// agent 应原样把每个 token 转发出去，最后 emit Done 携带完整文本。
func TestReActAgent_RunStream_NoToolCall(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ContentDelta: "你"},
				{ContentDelta: "好"},
				{ContentDelta: "！"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{}, // 同步接口不会被流式路径调用，仅满足 config 必填
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "你好"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 预期：3 个 Token + 1 个 LLMCall + 1 个 Final(C5 回复轮标记) + 1 个 Done
	require.Len(t, events, 6)
	assert.Equal(t, AgentEventToken, events[0].Type)
	assert.Equal(t, "你", events[0].Content)
	assert.Equal(t, "好", events[1].Content)
	assert.Equal(t, "！", events[2].Content)
	// 无工具的简单回答也产出一个 llm_call 步骤
	assert.Equal(t, AgentEventLLMCall, events[3].Type)
	assert.Equal(t, StepStatusSuccess, events[3].StepStatus)
	assert.NotEmpty(t, events[3].LLMRequest)
	assert.Contains(t, events[3].LLMResponse, "你好！")
	// C5:回复轮发 Final,携带完整回复文本
	assert.Equal(t, AgentEventFinal, events[4].Type)
	assert.Equal(t, "你好！", events[4].Content)
	assert.Equal(t, AgentEventDone, events[5].Type)
	assert.Equal(t, "你好！", events[5].Content)
}

// TestReActAgent_RunStream_SingleToolCall：LLM 第一轮决定调用一个工具，第二轮输出文本回答。
// agent 应 emit ToolCall + ToolResult，然后流式 token，最后 Done。
func TestReActAgent_RunStream_SingleToolCall(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// 第一轮：tool_call delta 跨多个 chunk
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "call_1", Name: "get_current_time"}},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// 第二轮：纯文本回答
			{
				{ContentDelta: "现在是 "},
				{ContentDelta: "10:30"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:         "get_current_time",
		Description:  "Get current time",
		DisplayLabel: "查询了当前时间",
		Parameters:   map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "2026-06-17 10:30:00 CST", nil
		},
	})
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "现在几点？"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 预期事件序列（v1.5.5）：LLMCall(决定调工具) → ToolCall → ToolResult → Token → Token → LLMCall(最终) → Done
	require.GreaterOrEqual(t, len(events), 9)
	// round1 的 llm_call：response 含 tool_calls
	assert.Equal(t, AgentEventLLMCall, events[0].Type)
	assert.Equal(t, StepStatusSuccess, events[0].StepStatus)
	assert.Contains(t, events[0].LLMResponse, "get_current_time")

	// 方案5:思考轮 LLMCall 后发 Thought(round1 无文本,Content 为空)
	assert.Equal(t, AgentEventThought, events[1].Type)

	assert.Equal(t, AgentEventToolCall, events[2].Type)
	assert.Equal(t, "get_current_time", events[2].ToolName)
	assert.Equal(t, "查询了当前时间", events[2].ToolLabel)

	assert.Equal(t, AgentEventToolResult, events[3].Type)
	assert.Equal(t, "get_current_time", events[3].ToolName)
	assert.Equal(t, "2026-06-17 10:30:00 CST", events[3].ToolResult)
	assert.Equal(t, StepStatusSuccess, events[3].StepStatus)
	assert.Equal(t, "{}", events[3].ToolArguments)
	assert.GreaterOrEqual(t, events[3].StepDurationMs, int64(0))

	// 后续是 token 流、round2 的 llm_call、Final(C5)、done
	assert.Equal(t, AgentEventToken, events[4].Type)
	assert.Equal(t, "现在是 ", events[4].Content)
	assert.Equal(t, AgentEventToken, events[5].Type)
	assert.Equal(t, "10:30", events[5].Content)
	assert.Equal(t, AgentEventLLMCall, events[6].Type)
	assert.Equal(t, AgentEventFinal, events[7].Type)
	assert.Equal(t, "现在是 10:30", events[7].Content)
	assert.Equal(t, AgentEventDone, events[8].Type)
	assert.Equal(t, "现在是 10:30", events[8].Content)
}

// TestReActAgent_RunStream_MultipleToolCalls：LLM 连续调用两次工具再给最终回答。
// 验证 ReAct 循环能跨轮串联，事件按时序输出。
func TestReActAgent_RunStream_MultipleToolCalls(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "tool_a", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c2", Name: "tool_b", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "完成"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name: "tool_a", DisplayLabel: "执行了工具A",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "result_a", nil
		},
	})
	registry.Register(Tool{
		Name: "tool_b", DisplayLabel: "执行了工具B",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "result_b", nil
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "ab"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 收集事件类型序列
	types := make([]AgentEventType, 0, len(events))
	for _, e := range events {
		types = append(types, e.Type)
	}

	expected := []AgentEventType{
		AgentEventLLMCall, AgentEventThought, AgentEventToolCall, AgentEventToolResult, // round1: tool_a
		AgentEventLLMCall, AgentEventThought, AgentEventToolCall, AgentEventToolResult, // round2: tool_b
		AgentEventToken,   // round3: "完成" token
		AgentEventLLMCall, // round3: llm_call 记录
		AgentEventFinal,   // C5: 回复轮发 Final
		AgentEventDone,
	}
	assert.Equal(t, expected, types)

	// 验证两次工具调用顺序正确(Thought 插入后 ToolCall 在 index 2 和 6)
	assert.Equal(t, "tool_a", events[2].ToolName)
	assert.Equal(t, "tool_b", events[6].ToolName)
	assert.Equal(t, "完成", events[8].Content)
}

// TestReActAgent_RunStream_FinalEventMarksReplyRound 思考模式 C5:回复轮(无 tool_call)
// 末必须发 AgentEventFinal,携带该轮完整文本;思考轮(有 tool_call)不发 Final。
// 模拟用户报告的 aihot 场景:思考文本+工具 -> 思考文本+工具 -> 最终回复。
func TestReActAgent_RunStream_FinalEventMarksReplyRound(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// round1: 思考文本 + 调 rss_reader
			{
				{ContentDelta: "我来读取这个网站。"},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "rss_reader", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// round2: 思考文本 + 再调 rss_reader
			{
				{ContentDelta: "让我尝试 RSS 地址。"},
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c2", Name: "rss_reader", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// round3: 最终回复(无工具)
			{
				{ContentDelta: "这是最新文章资讯。"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name: "rss_reader", DisplayLabel: "读取了 RSS 订阅",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "ok", nil
		},
	})

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "获取资讯"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 收集所有 Thought 事件(方案5):思考轮(round1/round2)各发 1 个,携带该轮思考文本。
	// 回复轮(round3)不发 Thought,发 Final。
	thoughts := []AgentEvent{}
	for _, e := range events {
		if e.Type == AgentEventThought {
			thoughts = append(thoughts, e)
		}
	}
	require.Len(t, thoughts, 2, "两个思考轮各发 1 个 Thought")
	assert.Equal(t, "我来读取这个网站。", thoughts[0].Content, "round1 思考文本")
	assert.Equal(t, "让我尝试 RSS 地址。", thoughts[1].Content, "round2 思考文本")

	// 收集所有 Final 事件:C5 下应恰好 1 个,且在回复轮(round3)发出
	finals := []AgentEvent{}
	for _, e := range events {
		if e.Type == AgentEventFinal {
			finals = append(finals, e)
		}
	}
	require.Len(t, finals, 1, "回复轮应发 1 个 Final 事件")
	assert.Equal(t, "这是最新文章资讯。", finals[0].Content, "Final 携带回复轮完整文本")

	// Final 必须在 Done 之前(回复轮末 -> Final -> Done)
	drainTypes := make([]AgentEventType, 0, len(events))
	for _, e := range events {
		drainTypes = append(drainTypes, e.Type)
	}
	finalIdx := -1
	doneIdx := -1
	for i, ty := range drainTypes {
		if ty == AgentEventFinal {
			finalIdx = i
		}
		if ty == AgentEventDone {
			doneIdx = i
		}
	}
	assert.Greater(t, finalIdx, -1, "有 Final")
	assert.Greater(t, doneIdx, finalIdx, "Final 在 Done 之前")

	// 思考轮(round1/round2)的 token 不应出现在 Final 里
	assert.NotContains(t, finals[0].Content, "我来读取这个网站")
	assert.NotContains(t, finals[0].Content, "让我尝试 RSS 地址")
}

// TestReActAgent_RunStream_ToolNotFound：LLM 调用了一个未注册的工具，
// agent 应把错误结果作为 tool_result 塞回继续推理，不应崩溃。
func TestReActAgent_RunStream_ToolNotFound(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "nonexistent", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "抱歉"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "x"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)

	// 第一个事件是 round1 的 llm_call（决定调工具），然后才是 ToolCall
	require.GreaterOrEqual(t, len(events), 4)
	assert.Equal(t, AgentEventLLMCall, events[0].Type)
	assert.Equal(t, AgentEventThought, events[1].Type)
	assert.Equal(t, AgentEventToolCall, events[2].Type)
	assert.Equal(t, "nonexistent", events[2].ToolName)

	// ToolResult 应携带"工具不存在"错误信息，status 为 not_found
	assert.Equal(t, AgentEventToolResult, events[3].Type)
	assert.Contains(t, events[3].ToolResult, "nonexistent")
	assert.Equal(t, StepStatusNotFound, events[3].StepStatus)

	// 最后应正常以 Done 结束
	assert.Equal(t, AgentEventDone, events[len(events)-1].Type)
}

// TestReActAgent_RunStream_ToolError：工具执行返回 error 时，ToolResult 事件 status 为 error，
// 且 ToolResult 携带原始未脱敏错误（供记录/分析，v1.5.5）。
func TestReActAgent_RunStream_ToolError(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "failing_tool", ArgumentsDelta: `{"x":1}`}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "失败了"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name: "failing_tool", DisplayLabel: "执行了工具",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "", context.DeadlineExceeded
		},
	})
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: llm,
		ToolRegistry:       registry,
		MaxSteps:           10,
		Timeout:            5 * time.Second,
	})

	ch, err := agent.RunStream(context.Background(), []map[string]interface{}{
		{"role": "user", "content": "x"},
	})
	require.NoError(t, err)

	events := drainEvents(t, ch)
	// 序列：LLMCall(决定调) → ToolCall → ToolResult → ... 工具步骤在 index 2
	require.GreaterOrEqual(t, len(events), 4)
	assert.Equal(t, AgentEventToolResult, events[3].Type)
	assert.Equal(t, StepStatusError, events[3].StepStatus)
	assert.Equal(t, `{"x":1}`, events[3].ToolArguments)
	// 原始错误透传（未脱敏）
	assert.Contains(t, events[3].ToolResult, context.DeadlineExceeded.Error())
}

// noopSyncLLM 仅用于满足 ReActAgentConfig.LLMClient 必填，流式路径不会调用它。
type noopSyncLLM struct{}

func (noopSyncLLM) ChatCompletion(
	ctx context.Context,
	messages []map[string]interface{},
	tools []map[string]interface{},
) (string, []map[string]interface{}, error) {
	return "", nil, nil
}
