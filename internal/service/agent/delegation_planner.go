package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	domainagent "omnibot/internal/domain/agent"
)

// DelegationTaskStarter 建后台任务的窄接口,SubAgentService 实现。
// 用窄接口而非 *SubAgentService,让规划器可独立测试(mock 桩)、不耦合服务全量方法。
type DelegationTaskStarter interface {
	StartTask(ctx context.Context, userID int64, subAgentType string, spec domainagent.TaskSpec, source, notifyTarget string) (int64, error)
}

// DelegatePlanItem 规划里的一条派活(与 delegate 工具参数同构,结构化为 plan_delegation 的 task 项)。
type DelegatePlanItem struct {
	SubAgentType       string                    `json:"sub_agent_type"`                // 子 Agent 类型(如 researcher)
	Goal               string                    `json:"goal"`                          // 委托目标(必填)
	Deliverables       []domainagent.Deliverable `json:"deliverables,omitempty"`        // 必须交付什么
	CompletionCriteria []string                  `json:"completion_criteria,omitempty"` // 完成标准
	Background         map[string]any            `json:"background,omitempty"`          // 背景信息
}

// DelegatePlan 规划 LLM 通过 plan_delegation 工具输出的(结构化)派活计划。tasks 可能为空(不派活)。
type DelegatePlan struct {
	Tasks []DelegatePlanItem `json:"tasks"`
}

// DelegationPlanner 结构化派活计划器(方向 B:治主 Agent 幻觉派活)。
//
// 背景:老方案让主 Agent 在 ReAct 循环里"自由发挥"决定要不要调 delegate 工具,该调没调
// 就产出"已安排X处理"却无任务——幻觉派活。任务标识(bbdcff6)只能事后暴露,不能事前拦截。
//
// 方案:主 Agent 回答**前**先跑一次专用规划调用(同步),LLM 用函数式输出一个结构化 JSON
// 计划(plan_delegation 工具的 tasks 数组,可为空)。框架**机械执行**该计划逐个 StartTask 建任务
// (代码建,LLM 跳过不了),取真实 task_id;再把"已创建哪些任务"作为事实注入上下文,主 Agent
// 据此写自然语言回复——回复有据可依,无法凭空说"已安排"。task_id 由框架拿到,主 Agent 篡改不了。
type DelegationPlanner struct {
	client   LLMClient             // 规划专用同步 LLM 调用(输出 JSON,不需流式)
	starter  DelegationTaskStarter // 建任务(SubAgentService),机械执行计划的入口
	registry *SubAgentRegistry     // 生成"可用子 Agent"清单,规划 prompt 告诉 LLM 能派给谁
}

// NewDelegationPlanner 创建规划器。
func NewDelegationPlanner(client LLMClient, starter DelegationTaskStarter, registry *SubAgentRegistry) *DelegationPlanner {
	return &DelegationPlanner{client: client, starter: starter, registry: registry}
}

// PlanAndExecute 一次完整规划:
//  1. 调规划 LLM -> 拿 plan_delegation 计划(结构化 JSON,可能空)
//  2. 机械执行计划:逐个 StartTask 建任务,收集真实 task_id
//  3. 生成注入上下文(告诉主 Agent 已创建哪些任务),供回答轮 grounding
//
// 返回值:
//   - taskIDs:本轮机械建出的任务 id(种子进 Runtime.DelegateTaskIDs,回复末尾拼接)
//   - injectedContext:注入对话的"已创建任务"事实;无任务时返回空串(调用方不注入)
//   - planStep:本次规划 LLM 调用的 StepRecord(LLMCall),供上层作为 agent_steps 记录——
//     规划这步是主 Agent ReAct 循环外的独立同步调用,不进循环事件流,需单独返回给上游落库。
//     规划调用失败也返回 planStep(status=error),复盘可见规划决策。nil 仅在调用方无法构造时。
//   - err:仅调用方无法构造 planStep 等系统错误才返回非 nil;规划返回空/解析失败均优雅降级,
//     以返回值表达(0 任务 + 空上下文),不报错。
func (p *DelegationPlanner) PlanAndExecute(ctx context.Context, userID int64, userMessage string) (taskIDs []int64, injectedContext string, planStep *StepRecord, err error) {
	messages := []map[string]interface{}{
		{"role": "system", "content": delegationPlannerPrompt(p.availableSubAgents())},
		{"role": "user", "content": userMessage},
	}
	start := time.Now()
	plan, toolCalls, callErr := p.callPlanner(ctx, messages)
	planStep = &StepRecord{
		Kind:       StepKindLLMCall,
		DurationMs: time.Since(start).Milliseconds(),
		Request:    marshalMessagesSnapshot(messages),
	}
	if callErr != nil {
		// 规划 LLM 调用失败:不阻塞主流程(0 任务),让主 Agent 正常回答。
		// 宁可这次不派活,也不要因规划失败挂掉整个对话。步骤记为 error(复盘可见)。
		planStep.Status = StepStatusError
		planStep.Response = callErr.Error()
		return nil, "", planStep, nil
	}
	planStep.Status = StepStatusSuccess
	planStep.Response = marshalLLMResponse("", toolCalls)

	items := make([]DelegatePlanItem, 0, len(plan.Tasks))
	for _, it := range plan.Tasks {
		if it.SubAgentType == "" || it.Goal == "" {
			continue // 非法项跳过(同 delegate 必填校验)
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		return nil, "", planStep, nil
	}

	source := getSourceFromContext(ctx)
	notifyTarget := getNotifyTargetFromContext(ctx)
	created := make([]int64, 0, len(items))
	for _, it := range items {
		tid, err := p.starter.StartTask(ctx, userID, it.SubAgentType, buildTaskSpecFromPlanItem(it), source, notifyTarget)
		if err != nil {
			// 单个任务建失败不阻断后续任务;已建的仍返回(回复可如实说明)。
			continue
		}
		created = append(created, tid)
	}
	if len(created) == 0 {
		return nil, "", planStep, nil
	}
	return created, BuildCreatedTasksContext(created, items), planStep, nil
}

// callPlanner 调规划 LLM,要求其调用 plan_delegation 工具输出结构化计划。
// 返回原始 tool_calls(供记录到 planStep.Response)。
// 无 tool_call / 非 plan_delegation / 参数非法时一律回落空计划(0 任务,不报错)。
func (p *DelegationPlanner) callPlanner(ctx context.Context, messages []map[string]interface{}) (*DelegatePlan, []map[string]interface{}, error) {
	_, toolCalls, err := p.client.ChatCompletion(ctx, messages, []map[string]interface{}{planningToolSchema})
	if err != nil {
		return nil, nil, err
	}
	if len(toolCalls) == 0 {
		return &DelegatePlan{}, toolCalls, nil
	}
	for _, raw := range toolCalls {
		call := parseToolCall(raw)
		if call.Name != "plan_delegation" {
			continue
		}
		plan := &DelegatePlan{}
		rawTasks, ok := call.Arguments["tasks"]
		if !ok {
			return plan, toolCalls, nil
		}
		arr, ok := rawTasks.([]interface{})
		if !ok {
			return plan, toolCalls, nil
		}
		for _, t := range arr {
			b, merr := json.Marshal(t)
			if merr != nil {
				continue
			}
			var item DelegatePlanItem
			if uerr := json.Unmarshal(b, &item); uerr != nil {
				continue
			}
			plan.Tasks = append(plan.Tasks, item)
		}
		return plan, toolCalls, nil
	}
	return &DelegatePlan{}, toolCalls, nil
}

// availableSubAgents 返回规划 prompt 里的"可用子 Agent"清单。
func (p *DelegationPlanner) availableSubAgents() string {
	if p.registry == nil {
		return "（暂无）"
	}
	return p.registry.DelegateToolDescription()
}

// buildTaskSpecFromPlanItem 把计划项转 TaskSpec(与 delegate 工具 Execute 同构)。
func buildTaskSpecFromPlanItem(it DelegatePlanItem) domainagent.TaskSpec {
	spec := domainagent.NewTaskSpec(it.Goal)
	spec.Deliverables = it.Deliverables
	spec.CompletionCriteria = it.CompletionCriteria
	if len(it.Background) > 0 {
		spec.Background = it.Background
	}
	return spec
}

// BuildCreatedTasksContext 生成注入主 Agent 对话的"已创建任务"事实。
// 告诉主 Agent 这些后台任务**确实已建好**,使其回复有据可依、无法凭空说"已安排"。
// 注意:只给目标/子Agent,不给数字 task_id——避免 LLM 回显"任务ID 53"(重复污染);
// 数字 task_id 由框架以独立 AgentEventTaskCreated 事件下发(前端渲染可点击卡片),不经过 LLM 文本。
func BuildCreatedTasksContext(created []int64, items []DelegatePlanItem) string {
	if len(created) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("[后台任务创建回执] 以下后台任务已由系统创建(程序创建,非口头安排):\n")
	for i := range created {
		goal := ""
		if i < len(items) {
			goal = items[i].Goal
		}
		fmt.Fprintf(&b, "- %s (子Agent:%s)\n", goal, subAgentTypeOf(items, i))
	}
	b.WriteString("请先告知用户已安排这些任务(用管家口吻),再回应当前消息。")
	return b.String()
}

func subAgentTypeOf(items []DelegatePlanItem, i int) string {
	if i < len(items) {
		return items[i].SubAgentType
	}
	return "?"
}

// lastUserMessage 从对话里取最后一条 user 消息文本,供规划器判断要不要派活。
// 无 user 消息或 content 非 string 返回空串(调用方不规划,幂等)。
func lastUserMessage(conversation []map[string]interface{}) string {
	for i := len(conversation) - 1; i >= 0; i-- {
		if role, _ := conversation[i]["role"].(string); role != "user" {
			continue
		}
		if content, ok := conversation[i]["content"].(string); ok {
			return content
		}
	}
	return ""
}

// planningToolSchema plan_delegation 工具 schema:唯一的参数是 tasks 数组(可为空)。
// 规划 LLM 必须调它输出结构化计划,避免自由文本 JSON 解析不稳。
var planningToolSchema = map[string]interface{}{
	"type": "function",
	"function": map[string]interface{}{
		"name":        "plan_delegation",
		"description": "输出本次需要派给子 Agent 后台执行的任务计划。不需要派活时 tasks 传空数组 []。",
		"parameters": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"tasks": map[string]interface{}{
					"type":        "array",
					"description": "待派任务列表。每项: sub_agent_type(必填,子Agent类型) + goal(必填,委托目标,清晰描述让子Agent做什么)。可带 deliverables(交付物[name,description]) / completion_criteria(完成标准数组) / background(背景对象)。",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"sub_agent_type":      map[string]interface{}{"type": "string", "description": "子 Agent 类型,如 researcher"},
							"goal":                map[string]interface{}{"type": "string", "description": "委托目标:清晰描述要让子 Agent 做什么"},
							"deliverables":        map[string]interface{}{"type": "array", "description": "必须交付的产物列表", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}}}},
							"completion_criteria": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}, "description": "完成标准列表"},
							"background":          map[string]interface{}{"type": "object", "description": "背景信息"},
						},
						"required": []string{"sub_agent_type", "goal"},
					},
				},
			},
			"required": []string{"tasks"},
		},
	},
}

// delegationPlannerPrompt 规划 LLM 的 system prompt:让 LLM 专注于"要不要派活 + 产出结构化计划"。
func delegationPlannerPrompt(subAgents string) string {
	return `你是任务规划器。当前用户消息可能包含需要后台子 Agent 执行的耗时研究/检索任务。
你必须用 plan_delegation 工具输出一个结构化派活计划(tasks 数组)。需要派活就逐条列出;不需要就传空数组 []。

【何时需要派活】用户请求属于以下任一类时,必须在计划里列出(不能直接由你回答):
- 研究/调研/了解某个主题或网站的最新内容
- 总结/汇总某网站的文章、资讯、动态
- 抓取或阅读某个网页的内容
- 任何需要联网获取实时/最新信息的请求

【可用子 Agent】
` + subAgents + `
【规则】
- 每项必须写清 sub_agent_type 和 goal(清晰、具体、让子 Agent 知道做什么)。
- 涉及多主题时拆成多条任务;同一主题合成一条,不要拆碎。
- 纯闲聊、纯解答常识、无需联网的任务 -> tasks 传空数组 []。`
}
