package agent

import "context"

// AgentServiceConfig 配置 Agent 服务。
type AgentServiceConfig struct {
	LLMClient          LLMClient          // 默认同步 LLM 客户端
	StreamingLLMClient StreamingLLMClient // 默认流式 LLM 客户端（与同步客户端通常是同一个对象）
	ToolRegistry       *ToolRegistry
	MaxSteps           int
	SystemPrompt       string
}

// AgentService 封装 ReActAgent，供 API 层调用。
type AgentService struct {
	defaultLLMClient    LLMClient
	defaultStreamClient StreamingLLMClient
	toolRegistry        *ToolRegistry
	maxSteps            int
	systemPrompt        string
}

// NewAgentService 创建 Agent 服务。
func NewAgentService(config AgentServiceConfig) *AgentService {
	return &AgentService{
		defaultLLMClient:    config.LLMClient,
		defaultStreamClient: config.StreamingLLMClient,
		toolRegistry:        config.ToolRegistry,
		maxSteps:            config.MaxSteps,
		systemPrompt:        config.SystemPrompt,
	}
}

// DefaultLLMClient 返回默认同步 LLM 客户端
func (s *AgentService) DefaultLLMClient() LLMClient {
	return s.defaultLLMClient
}

// DefaultStreamingLLMClient 返回默认流式 LLM 客户端
func (s *AgentService) DefaultStreamingLLMClient() StreamingLLMClient {
	return s.defaultStreamClient
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

// RunStream 流式执行 Agent。customStreamClient 非空时优先使用（适配用户自定义 LLM）。
// 返回的 channel 由 agent 内部 goroutine 关闭，调用方 range 消费即可。
//
// 同步 Run 的 customLLMClient 是 LLMClient 接口；这里因为流式协议不同，需要 StreamingLLMClient
// 接口。两者通常由同一个 OpenAILLMClient 对象实现。
func (s *AgentService) RunStream(ctx context.Context, userID int64, conversation []map[string]interface{}, customStreamClient ...StreamingLLMClient) (<-chan AgentEvent, error) {
	streamClient := s.defaultStreamClient
	if len(customStreamClient) > 0 && customStreamClient[0] != nil {
		streamClient = customStreamClient[0]
	}

	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          s.defaultLLMClient, // 占位，流式路径不会调用同步接口，但 NewReActAgent 不强制必填
		StreamingLLMClient: streamClient,
		ToolRegistry:       s.toolRegistry,
		MaxSteps:           s.maxSteps,
		SystemPrompt:       s.systemPrompt,
	})

	return agent.RunStream(withUserID(ctx, userID), conversation)
}
