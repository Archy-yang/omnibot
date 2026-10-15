package agent

// persistent_task_tool.go — manage_persistent_task 工具(25 号)。
// 主 Agent 创建/管理持久任务(周期调度);一次性任务仍走 delegate,两者语义不混。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	domainagent "omnibot/internal/domain/agent"
)

// CreateManagePersistentTaskTool 创建 manage_persistent_task 工具。
func CreateManagePersistentTaskTool(svc *PersistentTaskService) Tool {
	return Tool{
		Name:         "manage_persistent_task",
		DisplayLabel: "管理了周期任务",
		Description: "创建/管理持久任务(周期性自动执行)。" +
			"用户表达\"以后每天…/每周一…/持续关注…\"这类重复性诉求时用 create;" +
			"一次性任务用 delegate,不要混用。" +
			"actions: create(goal+cron 必填,可选 name/deliverables/completion_criteria/background/persona_hint/task_type," +
			"同 delegate 合同字段)/list/pause(id)/resume(id)/delete(id)。" +
			"cron 为 5 字段表达式(分 时 日 月 周,本地时区):\"每天早上9点\"→\"0 9 * * *\",\"每周一9点\"→\"0 9 * * 1\"。" +
			"铁律:周期与时刻必须来自用户原话;用户没说具体时间(如只说\"每天\")先确认再创建,禁止编造;" +
			"创建成功后向用户复述周期与下次执行时间,方便当场纠正。",
		Parameters: map[string]interface{}{
			"type": "object",
			"required": []string{"action"},
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type": "string", "enum": []string{"create", "list", "pause", "resume", "delete"},
					"description": "操作:create/list/pause/resume/delete",
				},
				"id":     map[string]interface{}{"type": "integer", "description": "持久任务 ID(pause/resume/delete 必填)"},
				"goal":   map[string]interface{}{"type": "string", "description": "每次执行的委托目标(create 必填)"},
				"name":   map[string]interface{}{"type": "string", "description": "短名(建议):4-12字,列表里显示"},
				"cron":   map[string]interface{}{"type": "string", "description": "5 字段 cron(create 必填,本地时区)"},
				"deliverables": map[string]interface{}{
					"type": "array", "description": "必须交付的产物列表,每项 {name, description}",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":        map[string]interface{}{"type": "string"},
							"description": map[string]interface{}{"type": "string"},
						},
					},
				},
				"completion_criteria": map[string]interface{}{
					"type": "array", "items": map[string]interface{}{"type": "string"},
					"description": "完成标准列表",
				},
				"background":   map[string]interface{}{"type": "object", "description": "背景信息,可空"},
				"persona_hint": map[string]interface{}{"type": "string", "description": "执行风格提示,可空"},
				"task_type":    map[string]interface{}{"type": "string", "description": "溯源标签(如'资讯''监控'),可空"},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			action, _ := args["action"].(string)
			userID := GetUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("manage_persistent_task: no user id in context")
			}

			switch action {
			case "create":
				return execCreatePersistentTask(ctx, svc, userID, args)
			case "list":
				return execListPersistentTasks(svc, userID)
			case "pause", "resume", "delete":
				return execControlPersistentTask(svc, userID, action, args)
			default:
				return "", fmt.Errorf("未知 action: %s", action)
			}
		},
	}
}

func execCreatePersistentTask(ctx context.Context, svc *PersistentTaskService, userID int64, args map[string]interface{}) (string, error) {
	goal, _ := args["goal"].(string)
	cronExpr, _ := args["cron"].(string)
	if goal == "" {
		return "", fmt.Errorf("create: goal 不能为空")
	}
	if cronExpr == "" {
		return "", fmt.Errorf("create: cron 不能为空(用户没说具体周期就先向用户确认)")
	}

	// 合同装配:与 delegate 同构(deliverables/criteria/background/persona/task_type)
	spec := domainagent.NewTaskSpec(goal)
	if name, _ := args["name"].(string); name != "" {
		spec.Name = name
	}
	if hint, _ := args["persona_hint"].(string); hint != "" {
		spec.PersonaHint = hint
	}
	if tt, _ := args["task_type"].(string); tt != "" {
		spec.Type = tt
	}
	if raw, ok := args["deliverables"].([]interface{}); ok {
		for _, it := range raw {
			if m, ok := it.(map[string]interface{}); ok {
				name, _ := m["name"].(string)
				desc, _ := m["description"].(string)
				if name != "" {
					spec.Deliverables = append(spec.Deliverables, domainagent.Deliverable{Name: name, Description: desc})
				}
			}
		}
	}
	if raw, ok := args["completion_criteria"].([]interface{}); ok {
		for _, it := range raw {
			if s, ok := it.(string); ok && s != "" {
				spec.CompletionCriteria = append(spec.CompletionCriteria, s)
			}
		}
	}
	if bg, ok := args["background"].(map[string]interface{}); ok && len(bg) > 0 {
		spec.Background = bg
	}
	specJSON, err := json.Marshal(spec)
	if err != nil {
		return "", err
	}

	// source/notify_target 从 ctx 取(handler 注入,同 delegate):渠道快照随任务落库,
	// 实例汇报继承;LLM 不经手,防编造推送目标
	source := getSourceFromContext(ctx)
	notifyTarget := getNotifyTargetFromContext(ctx)

	id, next, err := svc.Create(userID, CreatePersistentTaskInput{
		Name: spec.Name, Goal: goal, Cron: cronExpr,
		Source: source, NotifyTarget: notifyTarget, SpecJSON: string(specJSON),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("已创建持久任务 #%d「%s」\n周期: %s\n下次执行: %s\n请向用户复述以上周期与下次执行时间,确认无误;有偏差可暂停/删除重建。",
		id, spec.Name, cronExpr, next.Format("2006-01-02 15:04")), nil
}

func execListPersistentTasks(svc *PersistentTaskService, userID int64) (string, error) {
	list, err := svc.List(userID)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "暂无持久任务。", nil
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("共 %d 个持久任务:\n", len(list)))
	for _, pt := range list {
		state := "运行中"
		if !pt.Enabled {
			state = "已暂停"
		}
		b.WriteString(fmt.Sprintf("#%d「%s」[%s] 周期 %s · 下次执行 %s · 连续失败 %d\n",
			pt.ID, pt.Name, state, pt.Cron, pt.NextRunAt.Format("01-02 15:04"), pt.FailStreak))
	}
	return b.String(), nil
}

func execControlPersistentTask(svc *PersistentTaskService, userID int64, action string, args map[string]interface{}) (string, error) {
	idF, ok := args["id"].(float64)
	if !ok || idF <= 0 {
		return "", fmt.Errorf("%s: 需要 id", action)
	}
	id := int64(idF)
	var err error
	switch action {
	case "pause":
		err = svc.Pause(userID, id)
	case "resume":
		err = svc.Resume(userID, id)
	case "delete":
		err = svc.Delete(userID, id)
	}
	if err != nil {
		return "", err
	}
	verb := map[string]string{"pause": "已暂停", "resume": "已恢复", "delete": "已删除"}[action]
	out := fmt.Sprintf("持久任务 #%d %s。", id, verb)
	if action == "resume" {
		out += "下次执行时间已在列表中更新。"
	}
	return out, nil
}

// persistentTaskNotifyTargetKey 装配层注入 notify_target 的 ctx key
// (LLM 不经手,防编造推送目标)。
type persistentTaskNotifyTargetKey struct{}

var _ = time.Now // 保留 time 引用(确认文案格式化)
