package agent

import (
	"context"
	"fmt"
	"strconv"
)

// CreateQueryTaskTool 创建 query_task 工具:主 Agent 主动查任务状态/列表。
// 传 task_id 查单个;不传查该用户最近任务列表。补主 Agent 对任务的可观测性
// (之前只能被动读前置汇报回执,用户问"我的任务怎样了"答不上来)。
func CreateQueryTaskTool(svc *SubAgentService) Tool {
	return Tool{
		Name:         "query_task",
		DisplayLabel: "查询了任务",
		Description: "查询后台任务状态。传 task_id 查单个任务;不传查最近任务列表。" +
			"用户问\"我的任务怎样了\"\"派过什么任务\"时用此工具。",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "要查的任务 ID。不传则查该用户最近任务列表",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := getUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("query_task: no user id in context")
			}

			// 有 task_id:查单个
			if rawID, ok := args["task_id"]; ok && rawID != nil {
				taskID, err := parseTaskID(rawID)
				if err != nil {
					return "", err
				}
				summary, err := svc.QueryTask(userID, taskID)
				if err != nil {
					return "", fmt.Errorf("查询任务失败: %w", err)
				}
				return formatTaskSummary(summary), nil
			}

			// 无 task_id:查列表
			list, err := svc.ListUserTasks(userID, 10)
			if err != nil {
				return "", fmt.Errorf("查询任务列表失败: %w", err)
			}
			if len(list) == 0 {
				return "暂无任务", nil
			}
			out := fmt.Sprintf("最近 %d 个任务:\n", len(list))
			for _, s := range list {
				out += formatTaskSummary(s) + "\n"
			}
			return out, nil
		},
	}
}

// CreateCancelTaskTool 创建 cancel_task 工具:取消未结束的任务。
// pending/running 可取消;已结束(completed/failed/cancelled)拒绝。
// 用户说"不用查了""取消这个任务"时用此工具。
func CreateCancelTaskTool(svc *SubAgentService) Tool {
	return Tool{
		Name:         "cancel_task",
		DisplayLabel: "取消了任务",
		Description: "取消一个未结束的后台任务(pending 或 running 状态)。已完成的任务不可取消。" +
			"用户说\"不用查了\"\"取消\"时用此工具。",
		Parameters: map[string]interface{}{
			"type": "object",
			"required": []string{"task_id"},
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "要取消的任务 ID",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := getUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("cancel_task: no user id in context")
			}
			taskID, err := parseTaskID(args["task_id"])
			if err != nil {
				return "", err
			}
			if err := svc.CancelTask(userID, taskID); err != nil {
				return "", fmt.Errorf("取消任务失败: %w", err)
			}
			return fmt.Sprintf(`{"task_id": %d, "status": "cancelled", "message": "任务已取消"}`, taskID), nil
		},
	}
}

// CreateUpdateTaskTool 创建 update_task 工具:更新/补充任务信息。
// pending 态改 goal;running 态追加 note(补充信息,子 Agent 下轮注入上下文)。
// 用户对已派任务补充需求(如"顺便也查 X")时用此工具。
func CreateUpdateTaskTool(svc *SubAgentService) Tool {
	return Tool{
		Name:         "update_task",
		DisplayLabel: "更新了任务",
		Description: "更新或补充一个未结束任务的信息。pending 任务可改 goal;" +
			"running 任务可追加补充信息(note),子 Agent 下一轮会读到并入推理。" +
			"用户对已派任务补充需求时用此工具。",
		Parameters: map[string]interface{}{
			"type": "object",
			"required": []string{"task_id"},
			"properties": map[string]interface{}{
				"task_id": map[string]interface{}{
					"type":        "integer",
					"description": "要更新的任务 ID",
				},
				"goal": map[string]interface{}{
					"type":        "string",
					"description": "新的任务目标(仅 pending 任务可改;running 任务忽略此参数)",
				},
				"note": map[string]interface{}{
					"type":        "string",
					"description": "补充信息(running 任务用,会注入子 Agent 下一轮上下文)",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := getUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("update_task: no user id in context")
			}
			taskID, err := parseTaskID(args["task_id"])
			if err != nil {
				return "", err
			}
			goal, _ := args["goal"].(string)
			note, _ := args["note"].(string)
			if goal == "" && note == "" {
				return "", fmt.Errorf("update_task: goal 或 note 至少传一个")
			}
			if err := svc.UpdateTask(userID, taskID, goal, note); err != nil {
				return "", fmt.Errorf("更新任务失败: %w", err)
			}
			msg := "任务已更新"
			if note != "" {
				msg = fmt.Sprintf("已补充信息: %s", note)
			} else if goal != "" {
				msg = fmt.Sprintf("任务 goal 已更新: %s", goal)
			}
			return fmt.Sprintf(`{"task_id": %d, "message": %q}`, taskID, msg), nil
		},
	}
}

// parseTaskID 从工具参数解析 task_id(支持 float64/json number 或 string)。
func parseTaskID(raw interface{}) (int64, error) {
	switch v := raw.(type) {
	case float64:
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case string:
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("无效的 task_id: %s", v)
		}
		return id, nil
	}
	return 0, fmt.Errorf("task_id 缺失或类型无效")
}

// formatTaskSummary 格式化任务概要为给 LLM 读的文本。
func formatTaskSummary(s *TaskSummary) string {
	artifactHint := ""
	if s.Artifact != nil && *s.Artifact != "" {
		// 摘要前 60 字
		a := *s.Artifact
		if len(a) > 60 {
			a = a[:60] + "..."
		}
		artifactHint = fmt.Sprintf("\n  产出摘要: %s", a)
	}
	return fmt.Sprintf("任务 #%d [%s] %s\n  已执行 %d 步%s",
		s.ID, s.Status, truncateGoal(s.Goal, 50), s.StepCount, artifactHint)
}

// truncateGoal 截断 goal 摘要。
func truncateGoal(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
