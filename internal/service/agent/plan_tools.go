package agent

// plan_tools.go — 计划管理工具(34 号):manage_plan 查/取消/继续。
//
// 合并建模后"计划"就是一条任务,但对外仍用独立工具:
// 单任务用 query_task/cancel_task,整单计划用本工具,避免主 Agent 对容器误用单任务语义。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// PlanManager 计划管理窄接口(PlanRunner 实现;单测可替身)。
type PlanManager interface {
	Status(userID, planID int64) (*domainagent.AgentTask, []*domainagent.AgentTask, error)
	ListByUser(userID int64, limit int) ([]*domainagent.AgentTask, error)
	Cancel(ctx context.Context, userID, planID int64) error
	Resume(ctx context.Context, userID, planID int64) error
}

// CreateManagePlanTool 创建 manage_plan 工具。
func CreateManagePlanTool(plans PlanManager) Tool {
	return Tool{
		Name:         "manage_plan",
		DisplayLabel: "处理了任务计划",
		Description: "查看/取消/继续已拆分的计划(plan_id 来自 delegate 带 steps 时的返回值)。" +
			"action=status 查进度(不给 plan_id 返回最近几个计划);action=cancel 整单取消,当前步任务一并取消;" +
			"action=resume 让暂停(某步失败)的计划重试当前步。" +
			"注意:计划状态随时在变,必须实查,不要凭记忆回答。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"status", "cancel", "resume"},
					"description": "status=查进度;cancel=取消计划;resume=继续暂停的计划",
				},
				"plan_id": map[string]interface{}{
					"type":        "number",
					"description": "计划编号(delegate 返回的 plan_id)。cancel/resume 必填;status 可空(空则列最近计划)",
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := GetUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("manage_plan: no user id in context")
			}
			action, _ := args["action"].(string)
			planID := int64(0)
			if v, ok := args["plan_id"].(float64); ok {
				planID = int64(v)
			}

			switch action {
			case "status":
				if planID > 0 {
					plan, children, err := plans.Status(userID, planID)
					if err != nil {
						return "", planErrText(err)
					}
					return formatPlan(plan, children), nil
				}
				list, err := plans.ListByUser(userID, 5)
				if err != nil {
					return "", fmt.Errorf("查计划失败: %w", err)
				}
				if len(list) == 0 {
					return "还没有拆过计划。", nil
				}
				blocks := make([]string, 0, len(list))
				for _, p := range list {
					_, children, e := plans.Status(userID, p.ID)
					if e != nil {
						continue
					}
					blocks = append(blocks, formatPlan(p, children))
				}
				return strings.Join(blocks, "\n\n"), nil

			case "cancel":
				if planID <= 0 {
					return "", fmt.Errorf("cancel 需要 plan_id")
				}
				if err := plans.Cancel(ctx, userID, planID); err != nil {
					return "", planErrText(err)
				}
				return fmt.Sprintf("已取消计划 #%d,后续步骤不再执行(cancelled)。", planID), nil

			case "resume":
				if planID <= 0 {
					return "", fmt.Errorf("resume 需要 plan_id")
				}
				if err := plans.Resume(ctx, userID, planID); err != nil {
					return "", planErrText(err)
				}
				return fmt.Sprintf("已继续计划 #%d,重试当前步。", planID), nil

			default:
				return "", fmt.Errorf("未知 action: %q(可选 status/cancel/resume)", action)
			}
		},
	}
}

// formatPlan 计划的人读摘要(主 Agent 转述用,不要求机器可解析)。
func formatPlan(p *domainagent.AgentTask, children []*domainagent.AgentTask) string {
	if p == nil {
		return "计划不存在。"
	}
	total := len(children)
	done := 0
	for _, c := range children {
		if c.Status == domainagent.TaskStatusCompleted {
			done++
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "计划 #%d %s:%s(%d/%d)", p.ID, p.Name, p.Status, done, total)
	for _, c := range children {
		mark := " "
		if c.Status != domainagent.TaskStatusCompleted && c.Status != domainagent.TaskStatusCancelled {
			mark = ">"
		}
		fmt.Fprintf(&b, "\n%s %d. [%s] %s", mark, c.StepSeq, c.Status, c.Name)
		if c.Status == domainagent.TaskStatusFailed {
			b.WriteString("(失败,可 resume 重试)")
		}
	}
	return b.String()
}

// planErrText 把领域错误翻成主 Agent 能直接转述的中文。
func planErrText(err error) error {
	switch {
	case errors.Is(err, ErrPlanNotFound):
		return fmt.Errorf("计划不存在或不属于当前用户")
	case errors.Is(err, ErrPlanNotResumable):
		return fmt.Errorf("计划当前状态不可继续(只有暂停的计划能 resume)")
	case errors.Is(err, ErrPlanNotActive):
		return fmt.Errorf("计划已结束,不能取消")
	default:
		return fmt.Errorf("操作失败: %w", err)
	}
}
