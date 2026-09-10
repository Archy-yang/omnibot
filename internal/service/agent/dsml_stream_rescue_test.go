package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DSML 整轮聚合兜底测试(task#63 教训,12-记忆系统技术方案之外的独立修复):
// 模型把工具调用以 DSML 标记逐 token 拆碎在多个 delta 里流出时,
// 适配层逐 delta parseDSML 救不了(碎片不完整);循环聚合层必须对完整文本再兜底一次,
// 否则 DSML 原文会被当成"最终报告"存进 artifact。

func dsmlFragments(dsml string) []LLMStreamChunk {
	// 把完整 DSML 文本按 8 字节切碎,模拟 GLM 逐 token 流式输出
	var chunks []LLMStreamChunk
	b := []byte(dsml)
	for i := 0; i < len(b); i += 8 {
		end := i + 8
		if end > len(b) {
			end = len(b)
		}
		chunks = append(chunks, LLMStreamChunk{ContentDelta: string(b[i:end])})
	}
	chunks = append(chunks, LLMStreamChunk{FinishReason: "stop"}, LLMStreamChunk{Done: true})
	return chunks
}

// TestReActStream_DSMLFragmentedRescuedAsToolCall 碎片化 DSML → 聚合解析为工具调用,循环继续而非当最终答案。
func TestReActStream_DSMLFragmentedRescuedAsToolCall(t *testing.T) {
	dsml := "<｜DSML｜tool_calls>\n<｜DSML｜invoke name=\"web_read\">\n" +
		"<｜DSML｜parameter name=\"url\" string=\"true\">https://example.com</｜DSML｜parameter>" +
		"</｜DSML｜invoke>\n</｜DSML｜tool_calls>"

	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			dsmlFragments(dsml), // 轮1:碎片化 DSML(无 tool_calls 字段,finish=stop)
			{ // 轮2:工具结果回来后的正常最终回答
				{ContentDelta: "已核实,今日适合出行。"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}

	registry := NewToolRegistry()
	require.NoError(t, registry.Register(Tool{
		Name:        "web_read",
		Description: "test",
		Parameters:  map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		Execute: func(_ context.Context, _ map[string]interface{}) (string, error) {
			return "工具结果:天气晴", nil
		},
	}))

	svc := NewAgentService(AgentServiceConfig{
		StreamingLLMClient: llm,
		LLMClient:          noopSyncLLM{},
		ToolRegistry:       registry,
		SystemPrompt:       "测试",
	})

	eventCh, err := svc.RunStream(context.Background(), 42, []map[string]interface{}{
		{"role": "user", "content": "查天气"},
	}, llm)
	require.NoError(t, err)
	events := drainEvents(t, eventCh)

	var gotToolResult, gotFinal bool
	var finalContent string
	for _, ev := range events {
		switch ev.Type {
		case AgentEventToolResult:
			if ev.ToolName == "web_read" && strings.Contains(ev.ToolResult, "天气晴") {
				gotToolResult = true
			}
		case AgentEventFinal:
			gotFinal = true
			finalContent = ev.Content
		}
	}
	assert.True(t, gotToolResult, "DSML 应被聚合解析为真实工具调用并执行")
	assert.True(t, gotFinal, "工具执行后应有正常 Final 轮")
	assert.Equal(t, "已核实,今日适合出行。", finalContent)
	assert.NotContains(t, finalContent, "DSML", "最终回答不得含 DSML 标记")
}

// TestReActStream_NormalContentUnaffected 无 DSML 的正常流不受兜底影响(回归)。
func TestReActStream_NormalContentUnaffected(t *testing.T) {
	llm := &mockStreamingLLMClient{
		rounds: [][]LLMStreamChunk{
			{
				{ContentDelta: "今天天气不错"},
				{ContentDelta: ",适合出行。"},
				{FinishReason: "stop"},
				{Done: true},
			},
		},
	}
	svc := NewAgentService(AgentServiceConfig{
		StreamingLLMClient: llm,
		LLMClient:          noopSyncLLM{},
		ToolRegistry:       NewToolRegistry(),
		SystemPrompt:       "测试",
	})

	eventCh, err := svc.RunStream(context.Background(), 42, []map[string]interface{}{
		{"role": "user", "content": "你好"},
	}, llm)
	require.NoError(t, err)
	events := drainEvents(t, eventCh)

	var final string
	for _, ev := range events {
		if ev.Type == AgentEventFinal {
			final = ev.Content
		}
	}
	assert.Equal(t, "今天天气不错,适合出行。", final)
}
