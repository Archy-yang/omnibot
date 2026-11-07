package agent

// builtin_tools.go — 框架内建工具(delegate/query/update/cancel/request_input)
// 与工具上下文注入(ctx user_id/task_id/source)。领域工具(记忆/订阅/时间/计算器/
// RSS/web)已迁至子包 tools(omnibot/internal/service/agent/tools),由 wire.go 装配。

import (
	"context"
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/pkg/toolcore"
)

// 工具 ctx 键与注入/读取实现已下沉 internal/pkg/toolcore(2026-09-25,§7.3 下沉);
// 此处保留 agent 包内既有小写助手名,转发到 toolcore,包内调用零改动。

// UserIDContextKey 工具 ctx 中用户 id 的键(导出供 tools 子包测试构造 ctx)。
const UserIDContextKey = toolcore.UserIDKey

func withUserID(ctx context.Context, userID int64) context.Context {
	return toolcore.WithUserID(ctx, userID)
}

// withTaskID 把 taskID 注入 ctx(子 Agent runner 启动时调,供 request_input 工具取)。
func withTaskID(ctx context.Context, taskID int64) context.Context {
	return toolcore.WithTaskID(ctx, taskID)
}

// WithSource 把来源渠道注入 ctx(web/feishu handler 调,供 delegate 记录到 task.Source)。
// 导出供 handler 层调用(web/feishu 在调主 Agent Run 前注入)。
func WithSource(ctx context.Context, source string) context.Context {
	return toolcore.WithSource(ctx, source)
}

// WithNotifyTarget 把主动推送目标注入 ctx(feishu handler 注入 open_id,供 delegate 记录到 task.NotifyTarget)。
func WithNotifyTarget(ctx context.Context, target string) context.Context {
	return toolcore.WithNotifyTarget(ctx, target)
}

// WithTurnID 把当前逻辑 Turn 注入 ctx(Phase 1,16-架构迭代路线图 §5.4)。
// 实现位于 domain/agent(中立包,service/chat 也要读),此处转发保持 handler 单一 import 入口。
func WithTurnID(ctx context.Context, turnID int64) context.Context {
	return domainagent.WithTurnID(ctx, turnID)
}

func getSourceFromContext(ctx context.Context) string {
	return toolcore.SourceFromContext(ctx)
}

func getNotifyTargetFromContext(ctx context.Context) string {
	return toolcore.NotifyTargetFromContext(ctx)
}

func getTaskIDFromContext(ctx context.Context) int64 {
	return toolcore.TaskIDFromContext(ctx)
}

// WithMCPSearchCounter 初始化本回合的 MCP 搜索计数器(ReActAgent 每轮执行开始注入,
// mcp_search 工具读取并自增;运行时强制上限,非 prompt 约束)。
func WithMCPSearchCounter(ctx context.Context) context.Context {
	return toolcore.WithMCPSearchCounter(ctx)
}

// MCPSearchCounter 取计数器(未注入返回 nil,工具侧跳过限流)。
func MCPSearchCounter(ctx context.Context) *int32 {
	return toolcore.MCPSearchCounter(ctx)
}

func GetUserIDFromContext(ctx context.Context) int64 {
	return toolcore.UserIDFromContext(ctx)
}

// CreateDelegateTool 创建 delegate 工具(08 §4.4):主 Agent 通过它派活给子 Agent。
//
// 工具描述动态包含已注册子 Agent 的能力(从 registry.DelegateToolDescription 拼入),
// 主 Agent LLM 据此决定是否派活 + 派给谁。
//
// Execute 调 SubAgentService.StartTask,**立即返回** task_id(异步,不等子 Agent)。
// 工具结果给主 Agent LLM,让其生成自然语言确认("已安排X处理,稍后汇报")回用户。
//
// userID 从 ctx 取(主 Agent RunStream 已通过 withUserID 注入)。
//
// plans(可选)注入计划能力:传了 steps 时建计划并派第一步(34 号 §3.4);
// 未注入时 steps 参数直接报错,不静默降级成"只派第一步"。
func CreateDelegateTool(svc *SubAgentService, plans ...PlanCreator) Tool {
	var planCreator PlanCreator
	if len(plans) > 0 {
		planCreator = plans[0]
	}
	return Tool{
		Name:         "delegate",
		DisplayLabel: "安排了子任务",
		Description: "把耗时任务委派给后台执行器异步执行(不阻塞当前对话,通用不绑角色)。" +
			"委派 = goal(必) + name(短名) + deliverables(交付物) + completion_criteria(完成标准),可选 background/persona_hint。" +
			"派活后立即返回,执行器后台跑,完成后向用户汇报。适合需要多步检索/研究/汇总的耗时任务。" +
			"注意:本工具是创建后台任务的唯一方式,返回的 task_id 是唯一合法的任务编号——向用户提及任务编号时必须原样引用本工具的返回值。" +
			"有先后依赖、后一步要用前一步结果的活,用 steps 拆成 2-9 步交给计划按序执行(比一个 180 秒的大任务靠谱)," +
			"此时返回 plan_id,步骤状态用 manage_plan 查。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"goal": map[string]interface{}{
					"type":        "string",
					"description": "委托目标:清晰描述要让后台执行器做什么(必填)",
				},
				"name": map[string]interface{}{
					"type":        "string",
					"description": "任务短名(建议):4-12字,概括这单干什么,如'查AIHOT今日动态'。用户在任务列表里看到的是它",
				},
				"deliverables": map[string]interface{}{
					"type":        "array",
					"description": "必须交付的产物列表。每项 {name, description}。明确交付物让执行器知道要产出什么",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":        map[string]interface{}{"type": "string", "description": "交付物名"},
							"description": map[string]interface{}{"type": "string", "description": "交付物描述"},
						},
					},
				},
				"completion_criteria": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "完成标准列表(全部满足才算完成,达成后立即产出报告,不继续检索)。如['至少比较三个框架','给出明确推荐']",
				},
				"background": map[string]interface{}{
					"type":        "object",
					"description": "背景信息(项目/技术栈/当前架构等),帮助执行器理解上下文。可空",
				},
				"persona_hint": map[string]interface{}{
					"type":        "string",
					"description": "可选角色扮演提示:想让执行器以某角色/风格产出,一句话描述(如'你是严谨的研究员,先多路检索再出结构化报告+来源')。可空",
				},
				"task_type": map[string]interface{}{
					"type":        "string",
					"description": "可选任务类型标签(纯溯源/展示,如'研究''写代码'),不参与执行。可空",
				},
				"steps": map[string]interface{}{
					"type":        "array",
					"description": "可选计划步骤(2-9 步,按顺序自动执行)。只用于有先后依赖的活:后一步会拿到前一步的结果摘要。给了 steps 就不必再单独给 goal 之外的任务合同(每步自带)。",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":                map[string]interface{}{"type": "string", "description": "步骤短名(4-12字)"},
							"goal":                map[string]interface{}{"type": "string", "description": "本步目标(必填),写清这一步要产出什么"},
							"deliverables":        map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"name": map[string]interface{}{"type": "string"}, "description": map[string]interface{}{"type": "string"}}}},
							"completion_criteria": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						},
						"required": []string{"name", "goal"},
					},
				},
			},
			"required": []string{"goal"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			goal, _ := args["goal"].(string)
			if goal == "" {
				return "", fmt.Errorf("goal is required")
			}

			userID := GetUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("delegate: no user id in context")
			}

			// 计划路径(34 号):给了 steps 就建计划 + 派第一步,不再走单任务。
			if steps := parsePlanSteps(args["steps"]); len(steps) > 0 {
				return startPlannedTask(ctx, planCreator, userID, goal, args, steps)
			}

			taskSpec := domainagent.NewTaskSpec(goal)
			if name, _ := args["name"].(string); name != "" {
				taskSpec.Name = name
			}
			if persona, _ := args["persona_hint"].(string); persona != "" {
				taskSpec.PersonaHint = persona
			}
			if tt, _ := args["task_type"].(string); tt != "" {
				taskSpec.Type = tt
			}
			// deliverables
			if raw, ok := args["deliverables"]; ok {
				if arr, ok := raw.([]interface{}); ok {
					for _, it := range arr {
						if m, ok := it.(map[string]interface{}); ok {
							name, _ := m["name"].(string)
							desc, _ := m["description"].(string)
							if name != "" {
								taskSpec.Deliverables = append(taskSpec.Deliverables, domainagent.Deliverable{Name: name, Description: desc})
							}
						}
					}
				}
			}
			// completion_criteria
			if raw, ok := args["completion_criteria"]; ok {
				if arr, ok := raw.([]interface{}); ok {
					for _, it := range arr {
						if s, ok := it.(string); ok && s != "" {
							taskSpec.CompletionCriteria = append(taskSpec.CompletionCriteria, s)
						}
					}
				}
			}
			// background
			if raw, ok := args["background"]; ok {
				if m, ok := raw.(map[string]interface{}); ok && len(m) > 0 {
					bg := make(map[string]any, len(m))
					for k, v := range m {
						bg[k] = v
					}
					taskSpec.Background = bg
				}
			}

			// source/notifyTarget/turnID 从 ctx 取(handler 注入):
			// 前两者决定完成时往哪推送汇报;turnID 记录触发本任务的用户意图(§5.3)。
			source := getSourceFromContext(ctx)
			notifyTarget := getNotifyTargetFromContext(ctx)
			turnID := domainagent.TurnIDFromContext(ctx)
			taskID, err := svc.StartTask(ctx, userID, taskSpec, source, notifyTarget, turnID)
			if err != nil {
				return "", fmt.Errorf("派活失败: %w", err)
			}

			// 立即返回 task_id(异步),主 Agent LLM 据此回用户"已安排X处理"
			return fmt.Sprintf(`{"task_id": %d, "status": "pending", "message": "已安排后台执行器处理,稍后汇报"}`, taskID), nil
		},
	}
}

// parsePlanSteps 解析 steps 参数;不是数组或为空时返回 nil(退回单任务路径)。
// 无法识别的元素跳过,由 domain.ValidateSteps 统一兜底校验(步数/空字段)。
func parsePlanSteps(raw interface{}) []domainagent.PlanStepSpec {
	arr, ok := raw.([]interface{})
	if !ok || len(arr) == 0 {
		return nil
	}
	steps := make([]domainagent.PlanStepSpec, 0, len(arr))
	for _, it := range arr {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		goal, _ := m["goal"].(string)
		step := domainagent.PlanStepSpec{Name: name, Goal: goal}
		if dels, ok := m["deliverables"].([]interface{}); ok {
			for _, d := range dels {
				dm, ok := d.(map[string]interface{})
				if !ok {
					continue
				}
				dn, _ := dm["name"].(string)
				dd, _ := dm["description"].(string)
				if dn != "" {
					step.Deliverables = append(step.Deliverables, domainagent.Deliverable{Name: dn, Description: dd})
				}
			}
		}
		if crits, ok := m["completion_criteria"].([]interface{}); ok {
			for _, c := range crits {
				if s, ok := c.(string); ok && s != "" {
					step.CompletionCriteria = append(step.CompletionCriteria, s)
				}
			}
		}
		steps = append(steps, step)
	}
	return steps
}

// startPlannedTask 建计划并派第一步,返回给主 Agent 的 JSON(含 plan_id)。
func startPlannedTask(ctx context.Context, creator PlanCreator, userID int64, goal string, args map[string]interface{}, steps []domainagent.PlanStepSpec) (string, error) {
	if creator == nil {
		return "", fmt.Errorf("计划能力未装配:请去掉 steps 只派一个任务")
	}
	name, _ := args["name"].(string)
	if strings.TrimSpace(name) == "" {
		name = truncateGoal(goal, 20)
	}
	plan, err := creator.CreateAndStart(ctx, userID, name, goal, steps,
		getSourceFromContext(ctx), getNotifyTargetFromContext(ctx), domainagent.TurnIDFromContext(ctx))
	if err != nil {
		return "", fmt.Errorf("派计划失败: %w", err)
	}
	msg := fmt.Sprintf("已拆成 %d 步,逐步执行,做完一步说一步", len(steps))
	return fmt.Sprintf(`{"plan_id": %d, "steps": %d, "status": %q, "message": %q}`,
		plan.ID, len(steps), plan.Status, msg), nil
}
