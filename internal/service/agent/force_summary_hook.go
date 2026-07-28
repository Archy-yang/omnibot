package agent

import (
	"time"
)

// ForceSummaryHook MaxSteps 兜底强制汇总 hook(C)。
//
// 吸收原 runForcedSummary:达 MaxSteps 时不吐"已达到最大步数限制"废话,
// 而是强制做一次无工具 LLM 调用(tools=[]),让模型基于已收集信息产出报告。
// 失败(LLM 报错)返回空,交循环回落兜底文案。
//
// 持有 StreamingLLMClient 用于做汇总调用。
type ForceSummaryHook struct {
	streamClient StreamingLLMClient
}

// NewForceSummaryHook 创建强制汇总 hook。streamClient 用于 MaxSteps 兜底的汇总 LLM 调用。
func NewForceSummaryHook(streamClient StreamingLLMClient) *ForceSummaryHook {
	return &ForceSummaryHook{streamClient: streamClient}
}

// BeforeRound 汇总 hook 不参与工具过滤,原样返回。
func (h *ForceSummaryHook) BeforeRound(rt *Runtime) []map[string]interface{} { return rt.Tools }

// OnLLMResult 汇总 hook 不关心 LLM 结果,放行。
func (h *ForceSummaryHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	return true
}

// OnToolExecute 汇总 hook 不拦截工具执行。
func (h *ForceSummaryHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	return "", "", false
}

// OnMaxExhausted 达 MaxSteps 时强制做一次无工具 LLM 调用,返回汇总文本。
// 追加 user 提示明确要求立即产出报告;传空 tools 强制只能出文本(不能再调工具循环)。
// emit token(实时)+ llm_call 步骤。失败返回空串。
func (h *ForceSummaryHook) OnMaxExhausted(rt *Runtime) string {
	// 追加汇总提示(不修改 rt.Messages 原切片,避免影响循环状态)。
	summaryMessages := make([]map[string]interface{}, len(rt.Messages), len(rt.Messages)+1)
	copy(summaryMessages, rt.Messages)
	summaryMessages = append(summaryMessages, map[string]interface{}{
		"role":    "user",
		"content": "已达到工具调用步数上限,请立即基于以上已收集的信息,直接产出最终的研究报告/回答,不要再调用任何工具。",
	})

	reqSnapshot := marshalMessagesSnapshot(summaryMessages)
	roundStart := time.Now()

	chunkCh, err := h.streamClient.ChatCompletionStream(rt.Ctx, summaryMessages, nil) // tools=nil/空:强制只产出文本
	if err != nil {
		rt.Emit(AgentEvent{
			Type:           AgentEventLLMCall,
			LLMRequest:     reqSnapshot,
			LLMResponse:    "",
			StepStatus:     StepStatusError,
			StepDurationMs: time.Since(roundStart).Milliseconds(),
		})
		return ""
	}

	var content string
	for chunk := range chunkCh {
		if chunk.Error != nil {
			rt.Emit(AgentEvent{
				Type:           AgentEventLLMCall,
				LLMRequest:     reqSnapshot,
				LLMResponse:    content,
				StepStatus:     StepStatusError,
				StepDurationMs: time.Since(roundStart).Milliseconds(),
			})
			return ""
		}
		if chunk.Done {
			continue
		}
		if chunk.ContentDelta != "" {
			rt.Emit(AgentEvent{Type: AgentEventToken, Content: chunk.ContentDelta})
			content += chunk.ContentDelta
		}
	}

	rt.Emit(AgentEvent{
		Type:           AgentEventLLMCall,
		LLMRequest:     reqSnapshot,
		LLMResponse:    marshalLLMResponse(content, nil),
		StepStatus:     StepStatusSuccess,
		StepDurationMs: time.Since(roundStart).Milliseconds(),
	})
	return content
}
