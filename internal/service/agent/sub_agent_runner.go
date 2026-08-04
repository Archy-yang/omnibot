package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	llmConfigProvider   SubAgentLLMConfigProvider
	taskRepo            repoagent.AgentTaskRepository // 注入:装配 NoteInjectionHook 读 task.Notes
}

// NewSubAgentRunner 创建生产 SubAgentRunner。
// defaultClient 需同时实现 LLMClient 和 StreamingLLMClient(如 OpenAILLMClient)。
// llmConfigProvider 可为 nil(此时子 Agent 一律用系统默认)。
// taskRepo 可为 nil(此时不装配 NoteInjectionHook,running 态 update 的 notes 不注入)。
func NewSubAgentRunner(
	defaultSyncClient LLMClient,
	defaultStreamClient StreamingLLMClient,
	globalToolRegistry *ToolRegistry,
	llmConfigProvider SubAgentLLMConfigProvider,
	taskRepo repoagent.AgentTaskRepository,
) SubAgentRunner {
	return &subAgentRunnerImpl{
		defaultStreamClient: defaultStreamClient,
		defaultSyncClient:   defaultSyncClient,
		globalToolRegistry:  globalToolRegistry,
		llmConfigProvider:   llmConfigProvider,
		taskRepo:             taskRepo,
	}
}

func (r *subAgentRunnerImpl) Run(ctx context.Context, taskID, userID int64, card domainagent.SubAgentCard, taskSpec domainagent.TaskSpec, onStep func(StepRecord)) (string, error) {
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

	// 2. 填充 PromptTemplate 的 {goal} + 注入任务包详情(deliverables/criteria/constraints)
	//    让子 Agent 明确"做到什么程度算完",缓解循环不收敛(见 10-规划 §2.1)。
	systemPrompt := buildSubAgentPrompt(card.PromptTemplate, taskSpec)

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
	// + notes 注入(running 态 update_task 追加的补充信息,子 Agent 下轮读到并入推理)。
	hooks := []RoundHook{
		NewCircuitBreakerHook(ToolFailureThreshold),
		NewForceSummaryHook(streamClient),
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

// buildSubAgentPrompt 填充 PromptTemplate 的 {goal} + 注入任务包详情。
// taskSpec 含 deliverables/completion_criteria/constraints 时,追加结构化段让子 Agent
// 明确"必须交付什么/什么情况算完/有何约束",缓解循环不收敛。
// 仅 goal(无详情)时,等价于原 ReplaceAll({goal}),兼容老路径。
func buildSubAgentPrompt(template string, spec domainagent.TaskSpec) string {
	prompt := strings.ReplaceAll(template, "{goal}", spec.Goal)
	if !spec.HasDetail() {
		return prompt
	}

	var b strings.Builder
	b.WriteString(prompt)
	b.WriteString("\n\n== 任务合同(必须遵守)==\n")

	if len(spec.Deliverables) > 0 {
		b.WriteString("\n【必须交付】\n")
		for i, d := range spec.Deliverables {
			b.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, d.Name, d.Description))
		}
	}
	if len(spec.CompletionCriteria) > 0 {
		b.WriteString("\n【完成标准(全部满足才算完成,达成后立即产出报告,不要继续检索)】\n")
		for i, c := range spec.CompletionCriteria {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
		}
	}
	if len(spec.Background) > 0 {
		b.WriteString("\n【背景】\n")
		for k, v := range spec.Background {
			b.WriteString(fmt.Sprintf("- %s: %v\n", k, v))
		}
	}
	if spec.Constraints != nil {
		b.WriteString("\n【约束】\n")
		if spec.Constraints.MaxSteps > 0 {
			b.WriteString(fmt.Sprintf("- 最大步数: %d\n", spec.Constraints.MaxSteps))
		}
		if spec.Constraints.MaxToolCalls > 0 {
			b.WriteString(fmt.Sprintf("- 最大工具调用次数: %d\n", spec.Constraints.MaxToolCalls))
		}
		if !spec.Constraints.Deadline.IsZero() {
			b.WriteString(fmt.Sprintf("- 截止时间: %s\n", spec.Constraints.Deadline.Format("2006-01-02 15:04")))
		}
	}
	b.WriteString("\n注意:满足完成标准后必须立即产出报告,不要继续无意义检索。")
	return b.String()
}
