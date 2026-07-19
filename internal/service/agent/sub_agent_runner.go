package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	domainagent "omnibot/internal/domain/agent"
)

// SubAgentLLMConfigProvider 子 Agent 的用户 LLM 配置查询接口(方案3)。
// agent 包不直接依赖 service/user(分层洁癖),由 routes.go 用 user.LLMConfigService 适配注入。
// nil 时子 Agent 用系统默认 LLM(08 §5.1 原行为)。
type SubAgentLLMConfigProvider interface {
	// GetFullConfig 返回用户的完整 LLM 配置。hasConfig=false 表示用户未配(用系统默认)。
	GetFullConfig(userID int64) (apiKey, baseURL, model string, hasConfig bool, err error)
}

// subAgentRunnerImpl SubAgentRunner 的生产实现(08 §4.3 executeTask 用)。
//
// LLM 选型(方案3):优先用户自定义配置(用户花钱配的模型,子 Agent 该用),
// 无用户配置才回落系统默认。解决了「系统默认 key 空但用户有 key」的环境问题。
//
// 子 Agent 上下文隔离(08 §5.2):只拿 goal,不继承主对话历史。
type subAgentRunnerImpl struct {
	defaultStreamClient StreamingLLMClient
	defaultSyncClient   LLMClient
	globalToolRegistry  *ToolRegistry // 全局工具池,子 Agent 工具从这里选
	llmConfigProvider   SubAgentLLMConfigProvider
}

// NewSubAgentRunner 创建生产 SubAgentRunner。
// defaultClient 需同时实现 LLMClient 和 StreamingLLMClient(如 OpenAILLMClient)。
// llmConfigProvider 可为 nil(此时子 Agent 一律用系统默认)。
func NewSubAgentRunner(
	defaultSyncClient LLMClient,
	defaultStreamClient StreamingLLMClient,
	globalToolRegistry *ToolRegistry,
	llmConfigProvider SubAgentLLMConfigProvider,
) SubAgentRunner {
	return &subAgentRunnerImpl{
		defaultStreamClient: defaultStreamClient,
		defaultSyncClient:   defaultSyncClient,
		globalToolRegistry:  globalToolRegistry,
		llmConfigProvider:   llmConfigProvider,
	}
}

func (r *subAgentRunnerImpl) Run(ctx context.Context, userID int64, card domainagent.SubAgentCard, goal string) (string, []StepRecord, error) {
	// 1. 构造子 Agent 独立 ToolRegistry(从全局池选 card.Tools 指定的工具)
	subToolRegistry := NewToolRegistry()
	for _, toolName := range card.Tools {
		tool, ok := r.globalToolRegistry.Get(toolName)
		if !ok {
			return "", nil, fmt.Errorf("sub agent %q: tool %q not in global registry", card.Type, toolName)
		}
		if err := subToolRegistry.Register(tool); err != nil {
			return "", nil, fmt.Errorf("sub agent %q: register tool %q: %w", card.Type, toolName, err)
		}
	}

	// 2. 填充 PromptTemplate 的 {goal} -> 子 Agent system prompt
	systemPrompt := strings.ReplaceAll(card.PromptTemplate, "{goal}", goal)

	// 3. 选 LLM(方案3):优先用户配置,无则系统默认
	syncClient := r.defaultSyncClient
	streamClient := r.defaultStreamClient
	if r.llmConfigProvider != nil {
		if apiKey, baseURL, model, hasConfig, err := r.llmConfigProvider.GetFullConfig(userID); err == nil && hasConfig && apiKey != "" {
			// 子 Agent 后台跑(不阻塞用户),LLM 单请求超时给宽松些(60s),
			// 兼容响应较慢的服务商(如百度千帆首请求)。整体任务超时由 card.Timeout(180s)兜底。
			customClient := NewOpenAILLMClient(apiKey, baseURL, model, 60*time.Second)
			syncClient = customClient
			streamClient = customClient
		}
	}

	// 4. 构造 ReActAgent
	maxSteps := card.MaxSteps
	if maxSteps <= 0 {
		maxSteps = DefaultMaxSteps
	}
	timeout := card.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	ag := NewReActAgent(ReActAgentConfig{
		LLMClient:          syncClient,
		StreamingLLMClient: streamClient,
		ToolRegistry:       subToolRegistry,
		MaxSteps:           maxSteps,
		Timeout:            timeout,
		SystemPrompt:       systemPrompt,
	})

	// 5. 子 Agent 独立上下文:只有 system(已含 goal)+ 一条 user 触发。
	// 不继承主对话历史(08 §5.2 隔离)。userID 注入 ctx(供 search_memories 等按用户隔离工具)。
	conversation := []map[string]interface{}{
		{"role": "user", "content": goal},
	}

	// 6. 跑同步聚合 Run(内部 drain RunStream,返回 FinalResponse)
	result, err := ag.Run(withUserID(ctx, userID), conversation)
	if err != nil {
		return "", nil, err
	}
	return result.FinalResponse, result.Records, nil
}
