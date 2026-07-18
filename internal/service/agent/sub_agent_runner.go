package agent

import (
	"context"
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// subAgentRunnerImpl SubAgentRunner 的生产实现(08 §4.3 executeTask 用)。
//
// 用系统默认 LLM(不占用户配置,08 §5.1)+ 子 Agent card 指定的工具集(从全局工具池选),
// 构造独立 ReActAgent 跑一次,返回 FinalResponse 作为 Artifact。
//
// 子 Agent 上下文隔离(08 §5.2):只拿 goal,不继承主对话历史。
// userID 注入 ctx(供 search_memories/search_history 等按用户隔离的工具用)。
type subAgentRunnerImpl struct {
	defaultStreamClient StreamingLLMClient
	defaultSyncClient   LLMClient
	globalToolRegistry  *ToolRegistry // 全局工具池,子 Agent 工具从这里选
}

// NewSubAgentRunner 创建生产 SubAgentRunner。
// defaultClient 需同时实现 LLMClient 和 StreamingLLMClient(如 OpenAILLMClient)。
func NewSubAgentRunner(
	defaultSyncClient LLMClient,
	defaultStreamClient StreamingLLMClient,
	globalToolRegistry *ToolRegistry,
) SubAgentRunner {
	return &subAgentRunnerImpl{
		defaultStreamClient: defaultStreamClient,
		defaultSyncClient:   defaultSyncClient,
		globalToolRegistry:  globalToolRegistry,
	}
}

func (r *subAgentRunnerImpl) Run(ctx context.Context, card domainagent.SubAgentCard, goal string) (string, error) {
	// 1. 构造子 Agent 独立 ToolRegistry(从全局池选 card.Tools 指定的工具)
	subToolRegistry := NewToolRegistry()
	for _, toolName := range card.Tools {
		tool, ok := r.globalToolRegistry.Get(toolName)
		if !ok {
			return "", fmt.Errorf("sub agent %q: tool %q not in global registry", card.Type, toolName)
		}
		if err := subToolRegistry.Register(tool); err != nil {
			return "", fmt.Errorf("sub agent %q: register tool %q: %w", card.Type, toolName, err)
		}
	}

	// 2. 填充 PromptTemplate 的 {goal} -> 子 Agent system prompt
	systemPrompt := strings.ReplaceAll(card.PromptTemplate, "{goal}", goal)

	// 3. 构造 ReActAgent(系统默认 LLM + 子 Agent 工具集 + card 的 maxSteps/timeout)
	maxSteps := card.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	timeout := card.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ag := NewReActAgent(ReActAgentConfig{
		LLMClient:          r.defaultSyncClient,
		StreamingLLMClient: r.defaultStreamClient,
		ToolRegistry:       subToolRegistry,
		MaxSteps:           maxSteps,
		Timeout:            timeout,
		SystemPrompt:       systemPrompt,
	})

	// 4. 子 Agent 独立上下文:只有 system(已含 goal)+ 一条 user 触发。
	// 不继承主对话历史(08 §5.2 隔离)。
	conversation := []map[string]interface{}{
		{"role": "user", "content": goal},
	}

	// 5. 跑同步聚合 Run(内部 drain RunStream,返回 FinalResponse)
	result, err := ag.Run(ctx, conversation)
	if err != nil {
		return "", err
	}
	return result.FinalResponse, nil
}
