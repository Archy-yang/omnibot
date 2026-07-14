package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// LLMClient defines the interface for LLM chat completion with tool support.
type LLMClient interface {
	ChatCompletion(ctx context.Context, messages []map[string]interface{}, tools []map[string]interface{}) (content string, toolCalls []map[string]interface{}, err error)
}

// AgentStep 表示 Agent 循环中的一步
type AgentStep struct {
	StepNumber int
	ToolCall   *ToolCall
	ToolResult string
	Final      bool
}

// AgentResult Agent 执行结果。
//
// v1.6 起 `Records` 是同步 Run 路径的核心产出:Run 调 RunStream drain 事件后聚合成
// 有序 StepRecord 链(llm_call + tool_call),供上层落 agent_steps 复盘——和流式 handler
// 同款数据,做到「同步/流式两条返回形式,记录单一来源」。
//
// `Steps` 是 v1.5 ReActAgent.Run 老路径的产物(仅记 tool 调用,无 llm_call),保留只为
// 兼容 ReActAgent.Run 既有测试;AgentService.Run 走聚合路径后不再填充该字段。
type AgentResult struct {
	FinalResponse string
	Records       []StepRecord // v1.6: 聚合自 RunStream 事件的有序运行链
	Steps         []AgentStep  // Deprecated: 老 ReActAgent.Run 路径用,新代码看 Records
	TotalSteps    int
	Duration      time.Duration
}

// ReActAgentConfig Agent 配置
type ReActAgentConfig struct {
	LLMClient          LLMClient          // 同步路径（HandleSendMessageAgent 使用）
	StreamingLLMClient StreamingLLMClient // 流式路径（HandleSendMessageAgentStream 使用），可为空
	ToolRegistry       *ToolRegistry
	MaxSteps           int
	Timeout            time.Duration
	SystemPrompt       string
}

// 默认值
const (
	DefaultMaxSteps = 10
	DefaultTimeout  = 120 * time.Second
)

var defaultSystemPrompt = `You are a helpful AI assistant with access to tools.
When you need information, use the available tools to get it.
After receiving tool results, use them to provide a complete and helpful answer.
If a tool call fails, try a different approach or let the user know.`

// ReActAgent ReAct 模式 Agent
type ReActAgent struct {
	llmClient       LLMClient
	streamingClient StreamingLLMClient
	toolRegistry    *ToolRegistry
	maxSteps        int
	timeout         time.Duration
	systemPrompt    string
}

// NewReActAgent 创建 ReAct Agent
func NewReActAgent(config ReActAgentConfig) *ReActAgent {
	if config.MaxSteps <= 0 {
		config.MaxSteps = DefaultMaxSteps
	}
	if config.Timeout <= 0 {
		config.Timeout = DefaultTimeout
	}
	if config.SystemPrompt == "" {
		config.SystemPrompt = defaultSystemPrompt
	}
	return &ReActAgent{
		llmClient:       config.LLMClient,
		streamingClient: config.StreamingLLMClient,
		toolRegistry:    config.ToolRegistry,
		maxSteps:        config.MaxSteps,
		timeout:         config.Timeout,
		systemPrompt:    config.SystemPrompt,
	}
}

// Run 执行 Agent 循环
func (a *ReActAgent) Run(ctx context.Context, conversation []map[string]interface{}) (*AgentResult, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout)
	defer cancel()

	start := time.Now()

	messages := make([]map[string]interface{}, 0, len(conversation)+1)
	messages = append(messages, map[string]interface{}{
		"role":    "system",
		"content": a.systemPrompt,
	})
	messages = append(messages, conversation...)

	tools := a.toolRegistry.ToOpenAITools()
	var steps []AgentStep

	for stepNum := 1; stepNum <= a.maxSteps; stepNum++ {
		select {
		case <-ctx.Done():
			return a.buildFinalResult(steps, stepNum, "处理超时，已返回当前结果。", time.Since(start)), nil
		default:
		}

		content, toolCalls, err := a.llmClient.ChatCompletion(ctx, messages, tools)
		if err != nil {
			return nil, fmt.Errorf("agent step %d LLM call failed: %w", stepNum, err)
		}

		// 没有工具调用 — 返回最终回复
		if len(toolCalls) == 0 {
			return &AgentResult{
				FinalResponse: content,
				Steps:         steps,
				TotalSteps:    stepNum,
				Duration:      time.Since(start),
			}, nil
		}

		// 处理工具调用
		messages = append(messages, buildAssistantToolCallMessage(toolCalls))

		for _, tc := range toolCalls {
			toolCall := parseToolCall(tc)

			tool, ok := a.toolRegistry.Get(toolCall.Name)
			var toolResult string
			if !ok {
				toolResult = fmt.Sprintf("错误：工具 %q 不存在", toolCall.Name)
			} else {
				result, execErr := tool.Execute(ctx, toolCall.Arguments)
				if execErr != nil {
					toolResult = fmt.Sprintf("工具执行错误: %s", execErr.Error())
				} else {
					toolResult = result
				}
			}

			messages = append(messages, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": toolCall.ID,
				"content":      toolResult,
			})

			steps = append(steps, AgentStep{
				StepNumber: stepNum,
				ToolCall:   &toolCall,
				ToolResult: toolResult,
			})
		}
	}

	// 达到最大步数
	return &AgentResult{
		FinalResponse: "已达到最大步数限制。",
		Steps:         steps,
		TotalSteps:    a.maxSteps,
		Duration:      time.Since(start),
	}, nil
}

func (a *ReActAgent) buildFinalResult(steps []AgentStep, totalSteps int, fallback string, duration time.Duration) *AgentResult {
	return &AgentResult{
		FinalResponse: fallback,
		Steps:         steps,
		TotalSteps:    totalSteps,
		Duration:      duration,
	}
}

func parseToolCall(raw map[string]interface{}) ToolCall {
	id, _ := raw["id"].(string)
	fn, _ := raw["function"].(map[string]interface{})
	name, _ := fn["name"].(string)

	var args map[string]interface{}
	if argsStr, ok := fn["arguments"].(string); ok {
		err := json.Unmarshal([]byte(argsStr), &args)
		if err != nil {
			args = make(map[string]interface{})
			args["__parse_error"] = fmt.Sprintf("工具参数JSON解析失败: %s", err.Error())
		}
	}
	if args == nil {
		args = make(map[string]interface{})
	}

	return ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
	}
}

func buildAssistantToolCallMessage(toolCalls []map[string]interface{}) map[string]interface{} {
	oaiToolCalls := make([]map[string]interface{}, 0, len(toolCalls))
	for _, tc := range toolCalls {
		oaiToolCalls = append(oaiToolCalls, map[string]interface{}{
			"id":       tc["id"],
			"type":     "function",
			"function": tc["function"],
		})
	}
	return map[string]interface{}{
		"role":       "assistant",
		"content":    nil,
		"tool_calls": oaiToolCalls,
	}
}

// RunStream 执行流式 ReAct 循环。返回的 channel 由 agent 内部 goroutine 写入并关闭，
// 调用方只需 range 消费。channel 关闭前一定会有一个 AgentEventDone 或 AgentEventError 事件，
// 上游可据此判断流是否「正常结束」。
//
// 与同步 Run 的关键区别：
//   - 不返回完整 AgentResult（无 Steps 列表），步骤信息以事件形式实时流出
//   - 文本 token 一收到就立即转发，前端可立即渲染（解决 v1.5.0 那种「转圈 N 秒一次性吐」体验）
//   - 错误不通过返回值传递，而是用 AgentEventError 事件（除非 stream 都打不开就直接返 error）
func (a *ReActAgent) RunStream(ctx context.Context, conversation []map[string]interface{}) (<-chan AgentEvent, error) {
	if a.streamingClient == nil {
		return nil, fmt.Errorf("agent: streaming LLM client not configured")
	}

	out := make(chan AgentEvent, 16) // 小缓冲，避免生产者阻塞但不无限缓冲

	go func() {
		defer close(out)

		ctx, cancel := context.WithTimeout(ctx, a.timeout)
		defer cancel()

		messages := make([]map[string]interface{}, 0, len(conversation)+1)
		messages = append(messages, map[string]interface{}{
			"role":    "system",
			"content": a.systemPrompt,
		})
		messages = append(messages, conversation...)

		tools := a.toolRegistry.ToOpenAITools()

		// 累积本轮 ReAct 中所有 token 拼接成的最终回答，仅在最后没有 tool_call 时才有意义。
		// 用 string 拼接 OK，因为 LLM 单次回答的 token 总量有限（远小于 context window）。
		var finalAnswer string

		for stepNum := 1; stepNum <= a.maxSteps; stepNum++ {
			select {
			case <-ctx.Done():
				out <- AgentEvent{Type: AgentEventFinal, Content: "处理超时，已返回当前结果。"}
				out <- AgentEvent{Type: AgentEventDone, Content: "处理超时，已返回当前结果。"}
				return
			default:
			}

			// v1.5.5：快照本轮发给模型的 messages 作为 request 记录，并起计耗时。
			reqSnapshot := marshalMessagesSnapshot(messages)
			roundStart := time.Now()

			chunkCh, err := a.streamingClient.ChatCompletionStream(ctx, messages, tools)
			if err != nil {
				// LLM 调用打开失败也记一条 error 的 llm_call 步骤，再 emit Error。
				out <- AgentEvent{
					Type:           AgentEventLLMCall,
					LLMRequest:     reqSnapshot,
					LLMResponse:    "",
					StepStatus:     StepStatusError,
					StepDurationMs: time.Since(roundStart).Milliseconds(),
				}
				out <- AgentEvent{Type: AgentEventError, Error: fmt.Errorf("step %d: %w", stepNum, err)}
				return
			}

			// 本轮内累积：一段 LLM 响应可能是文本（emit token）或工具调用（按 index 累积 delta）。
			var roundContent string
			toolCallAccum := make(map[int]*toolCallAccumulator) // 按 index 索引

			for chunk := range chunkCh {
				if chunk.Error != nil {
					out <- AgentEvent{
						Type:           AgentEventLLMCall,
						LLMRequest:     reqSnapshot,
						LLMResponse:    roundContent,
						StepStatus:     StepStatusError,
						StepDurationMs: time.Since(roundStart).Milliseconds(),
					}
					out <- AgentEvent{Type: AgentEventError, Error: chunk.Error}
					return
				}
				if chunk.Done {
					continue // [DONE] 信号，channel 即将关闭，主循环靠 range 退出
				}
				if chunk.ContentDelta != "" {
					out <- AgentEvent{Type: AgentEventToken, Content: chunk.ContentDelta}
					roundContent += chunk.ContentDelta
				}
				if chunk.ToolCallDelta != nil {
					acc, ok := toolCallAccum[chunk.ToolCallDelta.Index]
					if !ok {
						acc = &toolCallAccumulator{}
						toolCallAccum[chunk.ToolCallDelta.Index] = acc
					}
					if chunk.ToolCallDelta.ID != "" {
						acc.id = chunk.ToolCallDelta.ID
					}
					if chunk.ToolCallDelta.Name != "" {
						acc.name = chunk.ToolCallDelta.Name
					}
					if chunk.ToolCallDelta.ArgumentsDelta != "" {
						acc.argumentsBuilder.WriteString(chunk.ToolCallDelta.ArgumentsDelta)
					}
				}
			}

			// 流结束。如果本轮没有工具调用 → ReAct 循环结束，本轮的 roundContent 就是最终回答。
			if len(toolCallAccum) == 0 {
				// v1.5.5：记一条 llm_call 步骤（response 是本轮文本回答）。
				out <- AgentEvent{
					Type:           AgentEventLLMCall,
					LLMRequest:     reqSnapshot,
					LLMResponse:    marshalLLMResponse(roundContent, nil),
					StepStatus:     StepStatusSuccess,
					StepDurationMs: time.Since(roundStart).Milliseconds(),
				}
				finalAnswer += roundContent
				// 思考模式 C5:回复轮(无 tool_call)发 Final,携带本轮完整文本(= 最终回复)。
				// 消费方据此明确区分思考与回复,不靠位置推断。
				out <- AgentEvent{Type: AgentEventFinal, Content: roundContent}
				out <- AgentEvent{Type: AgentEventDone, Content: finalAnswer}
				return
			}

			// 有工具调用。按 index 升序处理（map 无序，需排序）。
			indices := make([]int, 0, len(toolCallAccum))
			for idx := range toolCallAccum {
				indices = append(indices, idx)
			}
			// 简单插入排序（一般 1~2 个工具，不值得 sort 包开销）
			for i := 1; i < len(indices); i++ {
				for j := i; j > 0 && indices[j-1] > indices[j]; j-- {
					indices[j-1], indices[j] = indices[j], indices[j-1]
				}
			}

			// 构造塞回 messages 的 OpenAI 风格 tool_calls 数组（保持和同步 Run 一致的格式）
			rawToolCalls := make([]map[string]interface{}, 0, len(indices))
			for _, idx := range indices {
				acc := toolCallAccum[idx]
				rawToolCalls = append(rawToolCalls, map[string]interface{}{
					"id":   acc.id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      acc.name,
						"arguments": acc.argumentsBuilder.String(),
					},
				})
			}
			messages = append(messages, buildAssistantToolCallMessage(rawToolCalls))

			// v1.5.5：记一条 llm_call 步骤（response 是模型决定调用的 tool_calls）。
			out <- AgentEvent{
				Type:           AgentEventLLMCall,
				LLMRequest:     reqSnapshot,
				LLMResponse:    marshalLLMResponse(roundContent, rawToolCalls),
				StepStatus:     StepStatusSuccess,
				StepDurationMs: time.Since(roundStart).Milliseconds(),
			}

			// 逐个执行工具：先 emit ToolCall（用户友好的「正在调用 xxx」），再执行，再 emit ToolResult。
			for _, idx := range indices {
				acc := toolCallAccum[idx]
				toolCall := parseToolCall(map[string]interface{}{
					"id": acc.id,
					"function": map[string]interface{}{
						"name":      acc.name,
						"arguments": acc.argumentsBuilder.String(),
					},
				})

				tool, ok := a.toolRegistry.Get(toolCall.Name)
				label := toolCall.Name // 工具不存在时回落到英文名
				if ok && tool.DisplayLabel != "" {
					label = tool.DisplayLabel
				}

				out <- AgentEvent{
					Type:      AgentEventToolCall,
					ToolName:  toolCall.Name,
					ToolLabel: label,
				}

				// 执行工具并捕获记录字段（v1.5.5）：原始结果、状态、耗时、原始 arguments。
				var toolResult string
				var status string
				rawArgs := acc.argumentsBuilder.String()
				execStart := time.Now()
				if !ok {
					toolResult = fmt.Sprintf("错误：工具 %q 不存在", toolCall.Name)
					status = StepStatusNotFound
				} else {
					result, execErr := tool.Execute(ctx, toolCall.Arguments)
					if execErr != nil {
						toolResult = fmt.Sprintf("工具执行错误: %s", execErr.Error())
						status = StepStatusError
					} else {
						toolResult = result
						status = StepStatusSuccess
					}
				}
				durationMs := time.Since(execStart).Milliseconds()

				out <- AgentEvent{
					Type:           AgentEventToolResult,
					ToolName:       toolCall.Name,
					ToolResult:     toolResult,
					ToolArguments:  rawArgs,
					StepStatus:     status,
					StepDurationMs: durationMs,
				}

				messages = append(messages, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": toolCall.ID,
					"content":      toolResult,
				})
			}
			// 进入下一轮 ReAct，让 LLM 基于工具结果继续推理。
		}

		// 达到最大步数兜底
		out <- AgentEvent{Type: AgentEventFinal, Content: "已达到最大步数限制。"}
		out <- AgentEvent{Type: AgentEventDone, Content: "已达到最大步数限制。"}
	}()

	return out, nil
}

// toolCallAccumulator 累积单个 tool_call 跨 chunk 的增量数据。
type toolCallAccumulator struct {
	id               string
	name             string
	argumentsBuilder strings.Builder
}

// marshalMessagesSnapshot 把本轮发给模型的 messages 序列化为 JSON 字符串，作为
// agent_steps 的 llm_call request 记录（v1.5.5）。序列化失败返回空串，不阻断主流程。
func marshalMessagesSnapshot(messages []map[string]interface{}) string {
	b, err := json.Marshal(messages)
	if err != nil {
		return ""
	}
	return string(b)
}

// marshalLLMResponse 把模型本轮回复（文本 + 可选 tool_calls）序列化为 JSON 字符串，作为
// agent_steps 的 llm_call response 记录（v1.5.5）。
func marshalLLMResponse(content string, toolCalls []map[string]interface{}) string {
	payload := map[string]interface{}{"content": content}
	if len(toolCalls) > 0 {
		payload["tool_calls"] = toolCalls
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return string(b)
}
