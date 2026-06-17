package agent

import (
	"context"
	"encoding/json"
	"fmt"
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

// AgentResult Agent 执行结果
type AgentResult struct {
	FinalResponse string
	Steps         []AgentStep
	TotalSteps    int
	Duration      time.Duration
}

// ReActAgentConfig Agent 配置
type ReActAgentConfig struct {
	LLMClient    LLMClient
	ToolRegistry *ToolRegistry
	MaxSteps     int
	Timeout      time.Duration
	SystemPrompt string
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
	llmClient    LLMClient
	toolRegistry *ToolRegistry
	maxSteps     int
	timeout      time.Duration
	systemPrompt string
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
		llmClient:    config.LLMClient,
		toolRegistry: config.ToolRegistry,
		maxSteps:     config.MaxSteps,
		timeout:      config.Timeout,
		systemPrompt: config.SystemPrompt,
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
