package agent

import (
	"context"
	"fmt"
	"time"
)

// AgentServiceConfig 配置 Agent 服务。
type AgentServiceConfig struct {
	LLMClient          LLMClient          // 默认同步 LLM 客户端(目前仅用作类型契约占位,聚合路径不直接调用)
	StreamingLLMClient StreamingLLMClient // 默认流式 LLM 客户端,同步 Run 也从这里聚合
	ToolRegistry       *ToolRegistry
	MaxSteps           int
	SystemPrompt       string
	// Hooks 可插拔执行链(熔断/强制汇总等)。子 Agent 应装配[熔断+强制汇总];
	// 主 Agent 可不传(纯推理)或按需装配。nil=无机制(纯 ReAct 推理)。
	Hooks []RoundHook
	// DelegationPlanner 结构化派活规划器(方向 B)。仅主 Agent 装配;子 Agent 传 nil(不规划)。
	// 装配后,runStream 在进 ReAct 循环前先跑规划 -> 机械建后台任务 -> 注入上下文 + 种子 task_id。
	DelegationPlanner *DelegationPlanner
	// Timeout ReAct 循环的总超时(透传进 ReActAgentConfig.Timeout)。<=0 时 ReActAgent
	// 回落 DefaultTimeout(120s)。子 Agent 必须把 card.Timeout 传进来,否则内层循环用 120s
	// 强行截胡 executeTask 外层 ctx 更长的 deadline(见 51/52 超时 bug)。主 Agent 可不设(120s)。
	Timeout time.Duration
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
	hooks               []RoundHook
	delegationPlanner   *DelegationPlanner
	timeout             time.Duration
}

// NewAgentService 创建 Agent 服务。
func NewAgentService(config AgentServiceConfig) *AgentService {
	return &AgentService{
		defaultLLMClient:    config.LLMClient,
		defaultStreamClient: config.StreamingLLMClient,
		toolRegistry:        config.ToolRegistry,
		maxSteps:            config.MaxSteps,
		systemPrompt:        config.SystemPrompt,
		hooks:               config.Hooks,
		delegationPlanner:   config.DelegationPlanner,
		timeout:             config.Timeout,
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
			// token 是思考轮或回复轮的文本增量,IM 聚合路径不直接拼接(避免思考文本混入
			// FinalResponse)。最终回复由 AgentEventFinal 显式提供(C5)。
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
			// 聚合时只取 ToolResult 即可--它已带 ToolName/ToolArguments/原始 ToolResult/Status/Duration。
			records = append(records, StepRecord{
				Kind:       StepKindToolCall,
				Status:     ev.StepStatus,
				DurationMs: ev.StepDurationMs,
				Tool:       ev.ToolName,
				Request:    ev.ToolArguments,
				Response:   ev.ToolResult, // 原始未脱敏,展示脱敏由 handler 单独做
			})
		case AgentEventFinal:
			// C5:回复轮标记,Content 是最终回复。直接取,不靠 token 拼接/位置推断。
			finalContent = ev.Content
		case AgentEventDone:
			doneFallback = ev.Content // 仅在未收到 Final 时兜底(LLM 失败等异常)
		case AgentEventError:
			streamErr = ev.Error
		}
	}

	// err 时也返回已收集的 records(不丢执行过程):RunStream 失败前已 emit 的 llm_call/tool_call
	// 步骤对复盘至关重要,尤其子 Agent 失败任务--否则 executeTask 拿到 nil records 落不了库,
	// 与「失败也落步骤」矛盾。
	if streamErr != nil {
		return &AgentResult{
			FinalResponse: finalContent,
			Records:       records,
		}, fmt.Errorf("agent stream error: %w", streamErr)
	}
	// finalContent 由 AgentEventFinal 提供;未收到 Final(异常路径)回落 doneFallback。
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
// 主 Agent(装配了 DelegationPlanner)在进循环前先跑结构化规划(方向 B):
// 机械建后台任务 + 把"已创建哪些任务"注入上下文 + 把预建 task_id 种子进 Runtime。
// 规划那次 LLM 调用是循环外的独立同步调用,不会自然进事件流——前插一个 LLMCall 步骤,
// 使同步/流式 handler 都能把"规划决策"落进 agent_steps(复盘可见)。
func (s *AgentService) runStreamWithClient(
	ctx context.Context,
	userID int64,
	conversation []map[string]interface{},
	streamClient StreamingLLMClient,
) (<-chan AgentEvent, error) {
	if streamClient == nil {
		return nil, fmt.Errorf("agent: streaming LLM client not configured")
	}
	cfg := ReActAgentConfig{
		LLMClient:          s.defaultLLMClient, // 占位,流式路径不会调用同步接口
		StreamingLLMClient: streamClient,
		ToolRegistry:       s.toolRegistry,
		MaxSteps:           s.maxSteps,
		SystemPrompt:       s.systemPrompt,
		Timeout:            s.timeout, // 透传(P0):子 Agent 的 card.Timeout 才能真正约束循环
		Hooks:              s.hooks,
	}
	var preEvents []AgentEvent
	if s.delegationPlanner != nil {
		userMsg := lastUserMessage(conversation)
		if userMsg != "" {
			ids, injected, planStep, _ := s.delegationPlanner.PlanAndExecute(ctx, userID, userMsg)
			// 记录规划这次调用为 agent_steps 的 llm_call(成功/失败都记,复盘可见规划决策)。
			if planStep != nil {
				preEvents = append(preEvents, AgentEvent{
					Type:           AgentEventLLMCall,
					LLMRequest:     planStep.Request,
					LLMResponse:    planStep.Response,
					StepStatus:     planStep.Status,
					StepDurationMs: planStep.DurationMs,
				})
			}
			// 注入"已创建任务"上下文:告诉主 Agent 这些任务确实建好了(grounding,防幻觉派活)。
			if injected != "" {
				conversation = append([]map[string]interface{}{
					{"role": "system", "content": injected},
				}, conversation...)
			}
			// 预建 task_id 种子进 Runtime.DelegateTaskIDs,回复末尾拼接"任务ID: xxx"。
			cfg.PreCreatedTaskIDs = ids
		}
	}
	agent := NewReActAgent(cfg)
	child, err := agent.RunStream(withUserID(ctx, userID), conversation)
	if err != nil {
		return nil, err
	}
	if len(preEvents) == 0 {
		return child, nil
	}
	return prependEvents(preEvents, child), nil
}

// prependEvents 把 preEvents 作为前缀插入 child 事件流,再原样转发 child 后续事件。
// 用于把 ReAct 循环外的独立 LLM 调用(如结构化规划)记录为步骤,保持"同步/流式记录单一来源"。
// 转发顺序与原始通道一致;child 关闭后本通道才关闭,消费方 range 语义不变。
func prependEvents(preEvents []AgentEvent, child <-chan AgentEvent) <-chan AgentEvent {
	out := make(chan AgentEvent, len(preEvents)+16)
	go func() {
		defer close(out)
		for _, e := range preEvents {
			out <- e
		}
		for e := range child {
			out <- e
		}
	}()
	return out
}
