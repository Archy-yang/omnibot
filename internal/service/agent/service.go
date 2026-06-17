package agent

import "context"

// AgentServiceConfig 配置 Agent 服务。
type AgentServiceConfig struct {
	LLMClient    LLMClient // 默认LLM客户端
	ToolRegistry *ToolRegistry
	MaxSteps     int
	SystemPrompt string
}

// AgentService 封装 ReActAgent，供 API 层调用。
type AgentService struct {
	defaultLLMClient LLMClient
	toolRegistry     *ToolRegistry
	maxSteps         int
	systemPrompt     string
}

// NewAgentService 创建 Agent 服务。
func NewAgentService(config AgentServiceConfig) *AgentService {
	return &AgentService{
		defaultLLMClient: config.LLMClient,
		toolRegistry:     config.ToolRegistry,
		maxSteps:         config.MaxSteps,
		systemPrompt:     config.SystemPrompt,
	}
}

// DefaultLLMClient 返回默认LLM客户端
func (s *AgentService) DefaultLLMClient() LLMClient {
	return s.defaultLLMClient
}

// Run 执行 Agent，支持传入自定义 LLM 客户端（优先使用）
func (s *AgentService) Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...LLMClient) (*AgentResult, error) {
	llmClient := s.defaultLLMClient
	if len(customLLMClient) > 0 && customLLMClient[0] != nil {
		llmClient = customLLMClient[0]
	}

	// 为本次请求动态创建ReActAgent实例，使用对应LLM配置
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:    llmClient,
		ToolRegistry: s.toolRegistry,
		MaxSteps:     s.maxSteps,
		SystemPrompt: s.systemPrompt,
	})

	return agent.Run(withUserID(ctx, userID), conversation)
}
