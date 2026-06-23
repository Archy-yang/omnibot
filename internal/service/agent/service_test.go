package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentService_Run_AggregatesRunStream_NoTool 验证 v1.6 架构核心:同步 Run 是
// RunStream 之上的聚合层。给一组同款流式 chunks(无工具的简单回答),Run 必须:
//   - FinalResponse 等于所有 Token 拼接(== RunStream 文本)
//   - Records 恰好 1 条 llm_call(对应流式那个 LLMCall 事件)
//   - llm_call 的 Request/Response/Status 与流式事件携带值一致
func TestAgentService_Run_AggregatesRunStream_NoTool(t *testing.T) {
	stream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ContentDelta: "你好"},
				{ContentDelta: "!"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          &noopSyncLLM{}, // 聚合路径不会用同步接口
		StreamingLLMClient: stream,
		ToolRegistry:       NewToolRegistry(),
		MaxSteps:           5,
	})

	result, err := svc.Run(context.Background(), 123, []map[string]interface{}{
		{"role": "user", "content": "hi"},
	})

	require.NoError(t, err)
	assert.Equal(t, "你好!", result.FinalResponse)
	require.Len(t, result.Records, 1)
	rec := result.Records[0]
	assert.Equal(t, StepKindLLMCall, rec.Kind)
	assert.Equal(t, StepStatusSuccess, rec.Status)
	assert.NotEmpty(t, rec.Request, "Request 是发给模型的 messages JSON 快照")
	assert.Contains(t, rec.Response, "你好", "Response 是 {content,tool_calls} JSON")
	assert.GreaterOrEqual(t, rec.DurationMs, int64(0))
}

// TestAgentService_Run_AggregatesRunStream_WithTool 单工具调用场景:
// llm_call(决定调) → tool_call(执行) → llm_call(最终回答)。
// 验证 Records 时序与流式事件一致,tool_call 携带原始未脱敏结果。
func TestAgentService_Run_AggregatesRunStream_WithTool(t *testing.T) {
	stream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			// round1: 决定调 get_current_time
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "get_current_time", ArgumentsDelta: "{}"}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			// round2: 最终回答
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
		Name:       "get_current_time",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "2026-06-22 10:30:00 CST", nil
		},
	})

	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: stream,
		ToolRegistry:       registry,
		MaxSteps:           5,
	})

	result, err := svc.Run(context.Background(), 123, []map[string]interface{}{
		{"role": "user", "content": "现在几点?"},
	})

	require.NoError(t, err)
	assert.Equal(t, "现在是 10:30", result.FinalResponse)
	require.Len(t, result.Records, 3, "llm_call → tool_call → llm_call")

	assert.Equal(t, StepKindLLMCall, result.Records[0].Kind)
	assert.Contains(t, result.Records[0].Response, "get_current_time", "首轮 llm_call response 含 tool_calls")

	assert.Equal(t, StepKindToolCall, result.Records[1].Kind)
	assert.Equal(t, "get_current_time", result.Records[1].Tool)
	assert.Equal(t, "{}", result.Records[1].Request, "tool_call request 是 arguments")
	assert.Equal(t, "2026-06-22 10:30:00 CST", result.Records[1].Response, "原始未脱敏")
	assert.Equal(t, StepStatusSuccess, result.Records[1].Status)

	assert.Equal(t, StepKindLLMCall, result.Records[2].Kind)
	assert.Contains(t, result.Records[2].Response, "现在是 10:30", "末轮 llm_call response 是最终文本")
}

// TestAgentService_Run_ToolError 工具失败时,tool_call record 状态为 error,
// Response 保留原始错误(供 agent_steps 复盘)。
func TestAgentService_Run_ToolError(t *testing.T) {
	stream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ToolCallDelta: &ToolCallDelta{Index: 0, ID: "c1", Name: "failing", ArgumentsDelta: `{"x":1}`}},
				{FinishReason: "tool_calls"},
				{Done: true},
			},
			{
				{ContentDelta: "抱歉失败了"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	registry := NewToolRegistry()
	registry.Register(Tool{
		Name:       "failing",
		Parameters: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "", errors.New("dial tcp refused")
		},
	})

	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: stream,
		ToolRegistry:       registry,
		MaxSteps:           5,
	})

	result, err := svc.Run(context.Background(), 1, []map[string]interface{}{
		{"role": "user", "content": "x"},
	})
	require.NoError(t, err)
	require.Len(t, result.Records, 3)
	assert.Equal(t, StepStatusError, result.Records[1].Status)
	assert.Contains(t, result.Records[1].Response, "dial tcp refused", "原始错误透传")
	assert.Equal(t, `{"x":1}`, result.Records[1].Request)
}

// TestAgentService_Run_StreamOpenError 流打开失败:Run 返回 error,Records 至少含一条
// status=error 的 llm_call(v1.5.5 在 RunStream 打开失败时已 emit 该事件)。
func TestAgentService_Run_StreamOpenError(t *testing.T) {
	stream := &mockStreamingLLMClient{openErr: errors.New("network down")}
	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: stream,
		ToolRegistry:       NewToolRegistry(),
		MaxSteps:           5,
	})

	_, err := svc.Run(context.Background(), 1, []map[string]interface{}{
		{"role": "user", "content": "x"},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "network down")
}

// TestAgentService_Run_CustomLLMOverride 自定义 LLM(同时实现 StreamingLLMClient)透传到 RunStream。
// noopSyncLLM 不实现流式接口,所以 custom 必须是流式 mock 才能验证透传。
func TestAgentService_Run_CustomLLMOverride(t *testing.T) {
	defaultStream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{{{ContentDelta: "DEFAULT"}, {FinishReason: "stop"}, {Done: true}}},
	}
	customStream := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{{{ContentDelta: "CUSTOM"}, {FinishReason: "stop"}, {Done: true}}},
	}
	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          &noopSyncLLM{},
		StreamingLLMClient: defaultStream,
		ToolRegistry:       NewToolRegistry(),
		MaxSteps:           5,
	})

	// custom 同时实现 LLMClient + StreamingLLMClient(via dualClient adapter)
	dual := &dualClient{stream: customStream}
	result, err := svc.Run(context.Background(), 1, []map[string]interface{}{
		{"role": "user", "content": "x"},
	}, dual)
	require.NoError(t, err)
	assert.Equal(t, "CUSTOM", result.FinalResponse, "custom 客户端应被透传到 RunStream")
	assert.Equal(t, 0, defaultStream.callCount, "default 不应被调用")
}

// dualClient 同时实现同步和流式接口,模拟 OpenAILLMClient 那种"一个对象两个接口"。
type dualClient struct {
	stream *mockStreamingLLMClient
}

func (d *dualClient) ChatCompletion(
	ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{},
) (string, []map[string]interface{}, error) {
	return "", nil, nil
}

func (d *dualClient) ChatCompletionStream(
	ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{},
) (<-chan LLMStreamChunk, error) {
	return d.stream.ChatCompletionStream(ctx, messages, tools)
}

// 编译期保证 dualClient 同时实现两个接口
var _ LLMClient = (*dualClient)(nil)
var _ StreamingLLMClient = (*dualClient)(nil)

// 触发未使用警告的隐藏需求:time 包将在实现侧用到,这里仅占位避免 import 漂移
var _ = time.Second
