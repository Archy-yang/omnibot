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

	// toolFailureThreshold 同一工具连续失败达此次数后熔断(不再执行,返回禁用提示)。
	// 抑制子 Agent 对一直失败的工具(如 web_reader 对 401 站点)无限重试。
	toolFailureThreshold = 3
)

var defaultSystemPrompt = `You are a helpful AI assistant with access to tools.
When you need information, use the available tools to get it.
After receiving tool results, use them to provide a complete and helpful answer.
If a tool call fails, try a different approach or let the user know.`

// MainAgentSystemPrompt 构造主 Agent 的 system prompt(08 §4.4)。
// 在 defaultSystemPrompt 基础上增补:
//  1. 派活引导:耗时任务(研究/调研/多步检索)用 delegate 工具派子 Agent 后台执行,
//     派活后立即告诉用户"已安排",不要让用户干等。
//  2. 汇报引导:若上下文含[子任务完成回执],先汇报该结果再回应当前消息。
//
// hasSubAgents 表示是否装配了后台 Agent 框架(有 delegate 工具)。false 时回落默认 prompt。
func MainAgentSystemPrompt(hasSubAgents bool) string {
	if !hasSubAgents {
		return defaultSystemPrompt
	}
	return defaultSystemPrompt + `

== 派活规则(必须遵守)==
你有 delegate 工具,可以把任务委派给子 Agent(研究员)后台执行。

【什么是派活】派活 = **调用 delegate 工具**(Function Calling),不是口头说"已安排"。
你必须实际调用 delegate 工具(sub_agent_type + goal 参数),工具会返回 task_id。
只有拿到 task_id 后,你才可以说"已安排X处理"。

【硬规则】当用户请求属于以下任一类时,**必须调用 delegate 工具**,**禁止**自己直接回答:
- 研究/调研/了解某个主题或网站的最新内容(如"研究X""调研Y""了解Z的最新动态")
- 总结/汇总某网站的文章、资讯、动态
- 抓取或阅读某个网页的内容
- 任何需要联网获取实时信息的请求

【禁止行为】
- 禁止不调用 delegate 工具,却在回复里说"已安排/已派研究员/稍后汇报"--这是欺骗用户。
  没调工具就不能说已安排。
- 禁止凭训练知识直接回答以上类请求(知识可能过时/编造)。

【正确流程】
1. 识别到以上类请求 -> 第一步就调用 delegate 工具(sub_agent_type="researcher", goal=具体研究目标)
2. 工具返回 task_id 后 -> 用一句话告诉用户"已安排研究员处理X,稍后汇报"
3. 结束本轮回复。不要自己重复做子 Agent 会做的事。

== 汇报规则==
若对话上下文中出现[子任务完成回执],说明之前安排的子任务有结果了:
请先向用户汇报该任务的结果(用管家口吻转述,不要照搬回执格式),再回应用户当前的消息。`
}

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
		messages = append(messages, buildAssistantToolCallMessage(toolCalls, ""))

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

func buildAssistantToolCallMessage(toolCalls []map[string]interface{}, reasoning string) map[string]interface{} {
	oaiToolCalls := make([]map[string]interface{}, 0, len(toolCalls))
	for _, tc := range toolCalls {
		oaiToolCalls = append(oaiToolCalls, map[string]interface{}{
			"id":       tc["id"],
			"type":     "function",
			"function": tc["function"],
		})
	}
	msg := map[string]interface{}{
		"role":       "assistant",
		"content":    nil,
		"tool_calls": oaiToolCalls,
	}
	if reasoning != "" {
		msg["reasoning_content"] = reasoning // deepseek 思考模式:千帆要求多轮回传
	}
	return msg
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

		// 工具连续失败熔断(B):同一工具连续失败达 toolFailureThreshold 次,后续调用不再执行 Execute,
		// 直接返回熔断提示,迫使模型改路子。某次成功则清零该工具计数。抑制子 Agent 无限重试同类失败。
		toolFailStreak := make(map[string]int)

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

			// 熔断硬约束(B):本轮发给 LLM 的 tools 移除已熔断工具,让模型看不到它们。
			// 比"调用时返回提示"更狠--模型无法再发起该工具调用,被迫转向(见 task 18)。
			roundTools := filterToolsByCircuitBreaker(tools, toolFailStreak, toolFailureThreshold)
			chunkCh, err := a.streamingClient.ChatCompletionStream(ctx, messages, roundTools)
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
			var roundReasoning string // deepseek 思考模式:本轮思考过程,千帆要求多轮回传
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
				if chunk.ReasoningDelta != "" {
					roundReasoning += chunk.ReasoningDelta
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
			messages = append(messages, buildAssistantToolCallMessage(rawToolCalls, roundReasoning))

			// v1.5.5：记一条 llm_call 步骤（response 是模型决定调用的 tool_calls）。
			out <- AgentEvent{
				Type:           AgentEventLLMCall,
				LLMRequest:     reqSnapshot,
				LLMResponse:    marshalLLMResponse(roundContent, rawToolCalls),
				StepStatus:     StepStatusSuccess,
				StepDurationMs: time.Since(roundStart).Milliseconds(),
			}

			// 方案5:思考轮(有 tool_call)发 Thought,标记本轮文本是思考过程。
			// 前端据此把本轮 token 从主气泡迁移到思考块。Content 是本轮 LLM 文本。
			out <- AgentEvent{Type: AgentEventThought, Content: roundContent}

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
				rawArgs := acc.argumentsBuilder.String()

				tool, ok := a.toolRegistry.Get(toolCall.Name)
				label := toolCall.Name // 工具不存在时回落到英文名
				if ok && tool.DisplayLabel != "" {
					label = tool.DisplayLabel
				}

				out <- AgentEvent{
					Type:          AgentEventToolCall,
					ToolName:      toolCall.Name,
					ToolLabel:     label,
					ToolCallID:    toolCall.ID,
					ToolArguments: rawArgs,
				}

				// 执行工具并捕获记录字段（v1.5.5）：原始结果、状态、耗时、原始 arguments。
				var toolResult string
				var status string
				execStart := time.Now()
				if !ok {
					toolResult = fmt.Sprintf("错误：工具 %q 不存在", toolCall.Name)
					status = StepStatusNotFound
				} else if toolFailStreak[toolCall.Name] >= toolFailureThreshold {
					// 熔断(B):该工具已连续失败达阈值,不再执行,直接返回禁用提示。
					// 让模型读到"已禁用"从而改换思路/基于已有信息汇总,停止无效重试。
					toolResult = fmt.Sprintf("工具 %q 已连续失败 %d 次,已禁用。请改用其他来源或基于已有信息汇总,不要再调用该工具。",
						toolCall.Name, toolFailStreak[toolCall.Name])
					status = StepStatusError
				} else {
					result, execErr := tool.Execute(ctx, toolCall.Arguments)
					if execErr != nil {
						toolResult = fmt.Sprintf("工具执行错误: %s", execErr.Error())
						status = StepStatusError
						toolFailStreak[toolCall.Name]++
					} else {
						toolResult = result
						status = StepStatusSuccess
						// 成功一次清零连续失败计数(偶尔失败不误熔断)
						toolFailStreak[toolCall.Name] = 0
					}
				}
				durationMs := time.Since(execStart).Milliseconds()

				out <- AgentEvent{
					Type:           AgentEventToolResult,
					ToolName:       toolCall.Name,
					ToolResult:     toolResult,
					ToolArguments:  rawArgs,
					ToolCallID:     toolCall.ID,
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

		// 达到最大步数兜底:不吐"已达到最大步数限制"废话,而是强制做一次无工具 LLM 调用
		// (tools=[]),让模型基于已收集信息立即产出报告。保证最坏情况也有一份基于已有内容的报告,
		// 而非跑满步数却啥也没产出(见 task 15/17:15 轮检索后吐一句废话)。
		// 汇总失败(LLM 报错)才回落兜底文案。
		summary := a.runForcedSummary(ctx, messages, out)
		if summary == "" {
			summary = "已达到最大步数限制,未能生成汇总报告。"
		}
		out <- AgentEvent{Type: AgentEventFinal, Content: summary}
		out <- AgentEvent{Type: AgentEventDone, Content: summary}
	}()

	return out, nil
}

// runForcedSummary 在 MaxSteps 用尽时强制做一次无工具 LLM 调用(tools=[]),
// 让模型基于已收集的对话历史(messages)产出最终报告。emit token(实时)+ llm_call + 返回汇总文本。
// 失败(stream 打不开/chunk error/空内容)返回空串,由调用方回落兜底文案。
//
// 关键:传空 tools,强制模型只能产出文本,不能再调工具继续循环。
func (a *ReActAgent) runForcedSummary(ctx context.Context, messages []map[string]interface{}, out chan<- AgentEvent) string {
	// 追加一条 user 提示,明确要求模型立即汇总(不依赖模型自觉)。
	summaryMessages := make([]map[string]interface{}, len(messages), len(messages)+1)
	copy(summaryMessages, messages)
	summaryMessages = append(summaryMessages, map[string]interface{}{
		"role":    "user",
		"content": "已达到工具调用步数上限,请立即基于以上已收集的信息,直接产出最终的研究报告/回答,不要再调用任何工具。",
	})

	reqSnapshot := marshalMessagesSnapshot(summaryMessages)
	roundStart := time.Now()

	chunkCh, err := a.streamingClient.ChatCompletionStream(ctx, summaryMessages, nil) // tools=nil/空:强制只产出文本
	if err != nil {
		out <- AgentEvent{
			Type:           AgentEventLLMCall,
			LLMRequest:     reqSnapshot,
			LLMResponse:    "",
			StepStatus:     StepStatusError,
			StepDurationMs:  time.Since(roundStart).Milliseconds(),
		}
		return ""
	}

	var content string
	for chunk := range chunkCh {
		if chunk.Error != nil {
			out <- AgentEvent{
				Type:           AgentEventLLMCall,
				LLMRequest:     reqSnapshot,
				LLMResponse:    content,
				StepStatus:     StepStatusError,
				StepDurationMs: time.Since(roundStart).Milliseconds(),
			}
			return ""
		}
		if chunk.Done {
			continue
		}
		if chunk.ContentDelta != "" {
			out <- AgentEvent{Type: AgentEventToken, Content: chunk.ContentDelta}
			content += chunk.ContentDelta
		}
	}

	out <- AgentEvent{
		Type:           AgentEventLLMCall,
		LLMRequest:     reqSnapshot,
		LLMResponse:    marshalLLMResponse(content, nil),
		StepStatus:     StepStatusSuccess,
		StepDurationMs: time.Since(roundStart).Milliseconds(),
	}
	return content
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

// filterToolsByCircuitBreaker 从发给 LLM 的 tools 列表移除已熔断(连续失败达阈值)的工具。
// 让模型根本看不到该工具(硬约束),而非仅在调用时返回提示(软约束,模型仍会反复调,见 task 18)。
// 全部熔断时返回空,模型无工具可用,只能产出文本报告(与 C 强制汇总呼应)。
func filterToolsByCircuitBreaker(tools []map[string]interface{}, streak map[string]int, threshold int) []map[string]interface{} {
	filtered := make([]map[string]interface{}, 0, len(tools))
	for _, t := range tools {
		name := ""
		if fn, ok := t["function"].(map[string]interface{}); ok {
			if n, ok := fn["name"].(string); ok {
				name = n
			}
		}
		if streak[name] >= threshold {
			continue // 已熔断,移除
		}
		filtered = append(filtered, t)
	}
	return filtered
}
