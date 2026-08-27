package agent

import (
	"context"
	"fmt"
	"time"

	agentprompt "omnibot/internal/agentprompt"
	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
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
	allowedCapabilities []string      // 能力白名单:与工具能力标签交集决定子 Agent 可见工具集(仿 DSH ToolProviderResult)
	llmConfigProvider   SubAgentLLMConfigProvider
	taskRepo            repoagent.AgentTaskRepository // 注入:装配 NoteInjectionHook 读 task.Notes
}

// NewSubAgentRunner 创建生产 SubAgentRunner。
// defaultClient 需同时实现 LLMClient 和 StreamingLLMClient(如 OpenAILLMClient)。
// allowedCapabilities 为子 Agent 可见工具的能力白名单(空则回落 DefaultSubAgentCapabilities)。
// llmConfigProvider 可为 nil(此时子 Agent 一律用系统默认)。
// taskRepo 可为 nil(此时不装配 NoteInjectionHook,running 态 update 的 notes 不注入)。
func NewSubAgentRunner(
	defaultSyncClient LLMClient,
	defaultStreamClient StreamingLLMClient,
	globalToolRegistry *ToolRegistry,
	allowedCapabilities []string,
	llmConfigProvider SubAgentLLMConfigProvider,
	taskRepo repoagent.AgentTaskRepository,
) SubAgentRunner {
	if len(allowedCapabilities) == 0 {
		allowedCapabilities = DefaultSubAgentCapabilities
	}
	return &subAgentRunnerImpl{
		defaultStreamClient: defaultStreamClient,
		defaultSyncClient:   defaultSyncClient,
		globalToolRegistry:  globalToolRegistry,
		allowedCapabilities: allowedCapabilities,
		llmConfigProvider:   llmConfigProvider,
		taskRepo:            taskRepo,
	}
}

func (r *subAgentRunnerImpl) Run(ctx context.Context, taskID, userID int64, card domainagent.SubAgentCard, taskSpec domainagent.TaskSpec, onStep func(StepRecord)) (string, error) {
	// 1. 构造子 Agent 独立 ToolRegistry:能力标签 ∩ 配置能力白名单决定可见集(仿 DSH ToolProviderResult),
	//    取代旧的角色卡固定 card.Tools 列表(分类轴从"角色"下沉到"工具自身能力",可组合非枚举)。
	//    可见集含 alwaysBaseline(request_input),修复 #19:旧 card.Tools 从未含 request_input,子 Agent 实际调不到。
	subToolRegistry, _, err := BuildSubAgentToolRegistry(r.globalToolRegistry, r.allowedCapabilities)
	if err != nil {
		return "", fmt.Errorf("sub agent %q: %w", card.Type, err)
	}

	// 2. 经 agentprompt.PromptRegistry 组装子 Agent system prompt(11-Prompt管理 §5.2):填充 {goal} +
	//    注入任务包详情(deliverables/criteria/constraints),让子 Agent 明确"做到什么程度算完",
	//    缓解循环不收敛(见 10-规划 §2.1)。静态 section 组装不可能失败,故忽略 error。
	systemPrompt, _ := agentprompt.BuildSubAgentSystemPrompt(agentprompt.SubScope(card.Type), card.PromptTemplate, taskSpec)

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
	// 用 AgentService 聚合 Run(内部 drain RunStream,产生 Records + FinalResponse)。
	// 不能直接用 ReActAgent.Run(老路径不产生 Records,导致子 Agent 步骤落不了库)。
	// 子 Agent 装配执行链 hook:熔断(抑制对失败工具的无限重试)+ 强制汇总(MaxSteps 兜底出报告)
	// + 工具调用预算(总数达阈值移除所有工具,硬约束兜底 prompt 软约束"5来源上限",防超时)
	// + notes 注入(running 态 update_task 追加的补充信息,子 Agent 下轮读到并入推理)。
	hooks := []RoundHook{
		NewCircuitBreakerHook(ToolFailureThreshold),
		NewForceSummaryHook(streamClient),
		NewToolBudgetHook(ToolCallBudget),
	}
	if r.taskRepo != nil {
		hooks = append(hooks, NewNoteInjectionHook(taskID, r.taskRepo))
	}
	svc := NewAgentService(AgentServiceConfig{
		LLMClient:          syncClient,
		StreamingLLMClient: streamClient,
		ToolRegistry:       subToolRegistry,
		MaxSteps:           maxSteps,
		SystemPrompt:       systemPrompt,
		Hooks:              hooks,
		// P0:card.Timeout(如 180s)必须透传进 ReActAgent,否则内层循环用 DefaultTimeout 120s
		// 强行截胡 executeTask 外层 ctx 的更长超时(见 51/52 超时 bug)。
		Timeout: timeout,
	})

	// 5. 子 Agent 独立上下文:只有 system(已含任务合同)+ 一条 user 触发(= goal)。
	// 不继承主对话历史(08 §5.2 隔离)。userID 注入 ctx(供 search_memories 等按用户隔离工具)。
	conversation := []map[string]interface{}{
		{"role": "user", "content": taskSpec.Goal},
	}

	// 6. 流式执行 + 边跑边回吐步骤(onStep)。每产生一步立即回调上层落库,
	// 使任务 running 中即可观测执行过程(而非等结束批量落)。
	// userID/taskID 注入 ctx:userID 供 search_memories 等按用户隔离工具;
	// taskID 供 request_input 工具取(子 Agent 主动要输入时用,#19)。
	eventCh, err := svc.RunStream(withTaskID(ctx, taskID), userID, conversation, streamClient)
	if err != nil {
		return "", err
	}

	var finalContent string
	var doneFallback string
	var streamErr error
	for ev := range eventCh {
		switch ev.Type {
		case AgentEventLLMCall:
			if onStep != nil {
				onStep(StepRecord{
					Kind:       StepKindLLMCall,
					Status:     ev.StepStatus,
					DurationMs: ev.StepDurationMs,
					Request:    ev.LLMRequest,
					Response:   ev.LLMResponse,
				})
			}
		case AgentEventToolResult:
			// ToolResult 已带 ToolName/ToolArguments/原始结果/Status/Duration,
			// 聚合时只取 ToolResult 即可(RunStream 在其前发的 ToolCall 是状态条,不含结果)。
			if onStep != nil {
				onStep(StepRecord{
					Kind:       StepKindToolCall,
					Status:     ev.StepStatus,
					DurationMs: ev.StepDurationMs,
					Tool:       ev.ToolName,
					Request:    ev.ToolArguments,
					Response:   ev.ToolResult,
				})
			}
		case AgentEventFinal:
			finalContent = ev.Content
		case AgentEventDone:
			doneFallback = ev.Content
		case AgentEventError:
			streamErr = ev.Error
		}
	}

	// err 时步骤已随 onStep 实时落库(失败前已 emit 的步骤不丢),这里只返回错误。
	if streamErr != nil {
		return "", fmt.Errorf("agent stream error: %w", streamErr)
	}
	if finalContent == "" {
		finalContent = doneFallback
	}
	return finalContent, nil
}
