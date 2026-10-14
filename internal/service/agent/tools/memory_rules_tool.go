package tools

import (
	"context"
	"fmt"
	"strings"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/pkg/toolcore"
)

// MemoryRuleManager 沉淀黑名单管理(22号 §5.3;web 层同源接口,service 层实现)。
type MemoryRuleManager interface {
	ListBlacklist(userID int64) ([]*memorydomain.MemoryBlacklist, error)
	AddBlacklist(userID int64, pattern string) (*memorydomain.MemoryBlacklist, int64, error)
	RemoveBlacklist(userID, id int64) (bool, error)
}

// CreateMemoryRulesTool 沉淀黑名单管理工具:对话内承接"别记/忘掉 XX"。
func CreateMemoryRulesTool(svc MemoryRuleManager) toolcore.Tool {
	return toolcore.Tool{
		Name: "manage_memory_rules",
		Description: "管理记忆沉淀黑名单。当用户表达" +
			"\"别记/忘掉/以后不要记关于X的内容\"时用 add 添加规则(已有的相关记忆会被删除);" +
			"\"又可以记X了\"用 remove 解除。仅管理用户明确表达的禁止,不要自作主张添加。",
		DisplayLabel: "更新了记忆规则",
		Capabilities: []string{toolcore.CapMemory},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"add", "list", "remove"},
					"description": "add=禁止沉淀(可传存量删除条数);list=查看规则;remove=解除禁止",
				},
				"pattern": map[string]interface{}{
					"type":        "string",
					"description": "action=add 时必填。关键词/短语,尽量具体(过宽会误删大量记忆)",
				},
				"rule_id": map[string]interface{}{
					"type":        "number",
					"description": "action=remove 时必填。规则 ID(先 list 查询)",
				},
			},
			"required": []string{"action"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			action, _ := args["action"].(string)
			userID := toolcore.UserIDFromContext(ctx)
			switch action {
			case "list":
				rules, err := svc.ListBlacklist(userID)
				if err != nil {
					return "", fmt.Errorf("查询规则失败: %w", err)
				}
				if len(rules) == 0 {
					return "当前没有沉淀黑名单规则。", nil
				}
				var b strings.Builder
				b.WriteString("沉淀黑名单规则:\n")
				for _, r := range rules {
					fmt.Fprintf(&b, "- [%d] 不记包含\"%s\"的内容\n", r.ID, r.Pattern)
				}
				return strings.TrimRight(b.String(), "\n"), nil
			case "add":
				pattern, _ := args["pattern"].(string)
				if strings.TrimSpace(pattern) == "" {
					return "", fmt.Errorf("action=add 需要 pattern(要禁止的关键词/短语)")
				}
				rule, deleted, err := svc.AddBlacklist(userID, pattern)
				if err != nil {
					return "", err
				}
				if deleted > 0 {
					return fmt.Sprintf("已添加规则 #%d:不再记录包含\"%s\"的内容;同时删除了 %d 条既有相关记忆。",
						rule.ID, pattern, deleted), nil
				}
				return fmt.Sprintf("已添加规则 #%d:不再记录包含\"%s\"的内容(此前没有相关存量记忆)。", rule.ID, pattern), nil
			case "remove":
				id, ok := args["rule_id"].(float64)
				if !ok || id <= 0 {
					return "", fmt.Errorf("action=remove 需要 rule_id(先 list 查询)")
				}
				okDel, err := svc.RemoveBlacklist(userID, int64(id))
				if err != nil {
					return "", err
				}
				if !okDel {
					return "", fmt.Errorf("规则 #%d 不存在,可先 list 查询", int64(id))
				}
				return fmt.Sprintf("已解除规则 #%d:此后相关内容可以正常记录(此前删除的记忆不恢复)。", int64(id)), nil
			default:
				return "", fmt.Errorf("action 必须是 add/list/remove")
			}
		},
	}
}
