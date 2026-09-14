package agent

// builtin_tools.go — 框架内建工具(delegate/query/update/cancel/request_input)
// 与工具上下文注入(ctx user_id/task_id/source)。领域工具(记忆/订阅/时间/计算器/
// RSS/web)已迁至子包 tools(omnibot/internal/service/agent/tools),由 wire.go 装配。

import (
	"context"
	"fmt"

	domainagent "omnibot/internal/domain/agent"
)

type contextKey string

// UserIDContextKey 工具 ctx 中用户 id 的键(导出供 tools 子包测试构造 ctx)。

const (
	UserIDContextKey contextKey = "agent_user_id"
	taskIDContextKey contextKey = "agent_task_id"       // 子 Agent 运行时的 taskID(供 request_input 工具用)
	sourceContextKey contextKey = "agent_source"        // 任务来源渠道(web/feishu),供 delegate 记录到 task
	notifyContextKey contextKey = "agent_notify_target" // 主动推送目标(feishu=open_id),供 delegate 记录
)

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, UserIDContextKey, userID)
}

// withTaskID 把 taskID 注入 ctx(子 Agent runner 启动时调,供 request_input 工具取)。
func withTaskID(ctx context.Context, taskID int64) context.Context {
	return context.WithValue(ctx, taskIDContextKey, taskID)
}

// WithSource 把来源渠道注入 ctx(web/feishu handler 调,供 delegate 记录到 task.Source)。
// 导出供 handler 层调用(web/feishu 在调主 Agent Run 前注入)。
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceContextKey, source)
}

// WithNotifyTarget 把主动推送目标注入 ctx(feishu handler 注入 open_id,供 delegate 记录到 task.NotifyTarget)。
func WithNotifyTarget(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, notifyContextKey, target)
}

func getSourceFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(sourceContextKey).(string); ok {
		return s
	}
	return ""
}

func getNotifyTargetFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(notifyContextKey).(string); ok {
		return s
	}
	return ""
}

func getTaskIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(taskIDContextKey).(int64); ok {
		return id
	}
	return 0
}

func GetUserIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(UserIDContextKey).(int64); ok {
		return id
	}
	return 0
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
func CreateDelegateTool(svc *SubAgentService) Tool {
	return Tool{
		Name:         "delegate",
		DisplayLabel: "安排了子任务",
		Description: "把耗时任务委派给后台执行器异步执行(不阻塞当前对话,通用不绑角色)。" +
			"委派 = goal(必) + deliverables(交付物) + completion_criteria(完成标准),可选 background/persona_hint。" +
			"派活后立即返回,执行器后台跑,完成后向用户汇报。适合需要多步检索/研究/汇总的耗时任务。" +
			"注意:本工具是创建后台任务的唯一方式,返回的 task_id 是唯一合法的任务编号——向用户提及任务编号时必须原样引用本工具的返回值。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"goal": map[string]interface{}{
					"type":        "string",
					"description": "委托目标:清晰描述要让后台执行器做什么(必填)",
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

			taskSpec := domainagent.NewTaskSpec(goal)
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

			// source/notifyTarget 从 ctx 取(web/feishu handler 注入):决定完成时往哪推送汇报。
			source := getSourceFromContext(ctx)
			notifyTarget := getNotifyTargetFromContext(ctx)
			taskID, err := svc.StartTask(ctx, userID, taskSpec, source, notifyTarget)
			if err != nil {
				return "", fmt.Errorf("派活失败: %w", err)
			}

			// 立即返回 task_id(异步),主 Agent LLM 据此回用户"已安排X处理"
			return fmt.Sprintf(`{"task_id": %d, "status": "pending", "message": "已安排后台执行器处理,稍后汇报"}`, taskID), nil
		},
	}
}
