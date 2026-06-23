package agent

import (
	"context"
	"fmt"
)

// AgentServiceConfig 配置 Agent 服务。
type AgentServiceConfig struct {
	LLMClient          LLMClient          // 默认同步 LLM 客户端(目前仅用作类型契约占位,聚合路径不直接调用)
	StreamingLLMClient StreamingLLMClient // 默认流式 LLM 客户端,同步 Run 也从这里聚合
	ToolRegistry       *ToolRegistry
	MaxSteps           int
	SystemPrompt       string
}

// AgentService 封装 ReActAgent,供 API 层调用。
//
// v1.6 架构:底层只有 RunStream 一个真实实现,同步 Run 是 RunStream 上的一层聚合封装
// (drain 事件 channel 折叠为 AgentResult)。流式/同步只是返回形式不同,数据存储与运行链路
// 记录完全一致——记录逻辑只存在于 RunStream 单一来源,同步入口天然继承。
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

// DefaultLLMClient 返回默认同步 LLM 客户端。
//
// Deprecated(v1.6):同步 Run 已改为流式聚合,handler 选 LLM 时应统一传 StreamingLLMClient。
// 保留此方法仅为兼容尚未迁移的调用方(如 web 同步 handler 当前仍传 LLMClient,聚合内会做
// 类型断言适配)。
func (s *AgentService) DefaultLLMClient() LLMClient {
	return s.defaultLLMClient
}

// DefaultStreamingLLMClient 返回默认流式 LLM 客户端。
func (s *AgentService) DefaultStreamingLLMClient() StreamingLLMClient {
	return s.defaultStreamClient
}

// Run 执行 Agent(同步,返回聚合结果)。v1.6 起这是 RunStream 之上的聚合层:
//   - 内部调 RunStream 拿事件 channel
//   - drain 事件:Token 拼成 FinalResponse;LLMCall / ToolResult 折叠为 StepRecord
//   - 返回 AgentResult{FinalResponse, Records},Records 顺序与流式事件时序一致
//
// 与 RunStream 行为差异:同步 Run 不向调用方暴露 token / tool_call 中间事件,只给最终
// 文本 + 运行链路。适合微信、飞书等 IM 场景(无 SSE,但仍需复盘记录)。
//
// customLLMClient 是 variadic 可选:传 nil 或不传 → 用 default streaming client;
// 传非 nil 但**未实现 StreamingLLMClient** → 静默回退 default(生产路径 OpenAILLMClient
// 同时实现两接口,不会触发)。
func (s *AgentService) Run(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	customLLMClient ...LLMClient,
) (*AgentResult, error) {
	streamClient := s.defaultStreamClient
	if len(customLLMClient) > 0 && customLLMClient[0] != nil {
		if sc, ok := customLLMClient[0].(StreamingLLMClient); ok {
			streamClient = sc
		}
	}

	eventCh, err := s.runStreamWithClient(ctx, userID, conversation, streamClient)
	if err != nil {
		return nil, err
	}

	var (
		finalContent string
		doneFallback string
		records      []StepRecord
		streamErr    error
	)
	for ev := range eventCh {
		switch ev.Type {
		case AgentEventToken:
			finalContent += ev.Content
		case AgentEventLLMCall:
			records = append(records, StepRecord{
				Kind:       StepKindLLMCall,
				Status:     ev.StepStatus,
				DurationMs: ev.StepDurationMs,
				Request:    ev.LLMRequest,
				Response:   ev.LLMResponse,
			})
		case AgentEventToolResult:
			// 注:RunStream 在 emit ToolResult 前会先 emit 同名 ToolCall(用户友好状态条),
			// 聚合时只取 ToolResult 即可——它已带 ToolName/ToolArguments/原始 ToolResult/Status/Duration。
			records = append(records, StepRecord{
				Kind:       StepKindToolCall,
				Status:     ev.StepStatus,
				DurationMs: ev.StepDurationMs,
				Tool:       ev.ToolName,
				Request:    ev.ToolArguments,
				Response:   ev.ToolResult, // 原始未脱敏,展示脱敏由 handler 单独做
			})
		case AgentEventDone:
			doneFallback = ev.Content // 仅在 finalContent 为空时兜底(如超时/达到最大步数)
		case AgentEventError:
			streamErr = ev.Error
		}
	}

	if streamErr != nil {
		return nil, fmt.Errorf("agent stream error: %w", streamErr)
	}
	if finalContent == "" {
		finalContent = doneFallback
	}
	return &AgentResult{
		FinalResponse: finalContent,
		Records:       records,
	}, nil
}

// RunStream 流式执行 Agent。customStreamClient 非空时优先使用(适配用户自定义 LLM)。
// 返回的 channel 由 agent 内部 goroutine 关闭,调用方 range 消费即可。
func (s *AgentService) RunStream(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	customStreamClient ...StreamingLLMClient,
) (<-chan AgentEvent, error) {
	streamClient := s.defaultStreamClient
	if len(customStreamClient) > 0 && customStreamClient[0] != nil {
		streamClient = customStreamClient[0]
	}
	return s.runStreamWithClient(ctx, userID, conversation, streamClient)
}

// runStreamWithClient 是 Run / RunStream 共享的内部入口:构造 ReActAgent 并启动流式执行。
func (s *AgentService) runStreamWithClient(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	streamClient StreamingLLMClient,
) (<-chan AgentEvent, error) {
	agent := NewReActAgent(ReActAgentConfig{
		LLMClient:          s.defaultLLMClient, // 占位,流式路径不会调用同步接口
		StreamingLLMClient: streamClient,
		ToolRegistry:       s.toolRegistry,
		MaxSteps:           s.maxSteps,
		SystemPrompt:       s.systemPrompt,
	})
	return agent.RunStream(withUserID(ctx, userID), conversation)
}
