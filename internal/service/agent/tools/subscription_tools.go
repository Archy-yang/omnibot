package tools

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	subdomain "omnibot/internal/domain/subscription"
	agentpkg "omnibot/internal/service/agent"
)

// SubscriptionManager 订阅源管理(14-订阅源管理技术方案 §6.1)。
// agent 包只依赖此窄接口 + domain 类型,不 import service 实现(分层约束)。
type SubscriptionManager interface {
	Add(userID int64, siteURL, topicDesc string) (*subdomain.AddResult, error)
	List(userID int64) ([]*subdomain.Subscription, error)
	Remove(userID, id int64) error
	SetStatus(userID, id int64, status string) error
}

// CreateManageSubscriptionsTool 订阅源管理工具:对话即管理界面。
// 主 Agent 用它管理订阅(add/list/remove/pause/resume);子 Agent 研究用户
// 关注领域时用 list 取订阅清单选源(见 SubSourceRulesPrompt)。
func CreateManageSubscriptionsTool(svc SubscriptionManager) agentpkg.Tool {
	return agentpkg.Tool{
		Name:         "manage_subscriptions",
		Description:  "管理用户关注的 RSS 信息源。action=add 订阅(url 可以是网站/博客地址,工具自动发现其 RSS 地址,也可直接给 feed 地址);action=list 查看订阅清单(含每个源的主题描述);action=remove 退订;action=pause/resume 暂停/恢复(按 id)。用户说'订阅X/我关注X的博客/帮我盯着X'时调用 add;用户问'我订了哪些'时调用 list。",
		DisplayLabel: "管理了订阅源",
		Capabilities: []string{agentpkg.CapResearch, agentpkg.CapMemory},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]interface{}{
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"add", "list", "remove", "pause", "resume"},
					"description": "操作:add=订阅,list=查看清单,remove=退订,pause=暂停,resume=恢复",
				},
				"url": map[string]interface{}{
					"type":        "string",
					"description": "add 必填:网站/博客地址或 RSS 地址",
				},
				"topic_desc": map[string]interface{}{
					"type":        "string",
					"description": "add 可选:用户对这个源的主题描述(如'AI 领域动态'),缺省由源元数据生成",
				},
				"id": map[string]interface{}{
					"type":        "integer",
					"description": "remove/pause/resume 必填:订阅 id(来自 list 结果)",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			userID := agentpkg.GetUserIDFromContext(ctx)
			if userID == 0 {
				return "", fmt.Errorf("无法识别当前用户")
			}

			action, _ := args["action"].(string)
			switch action {
			case "add":
				siteURL, _ := args["url"].(string)
				topicDesc, _ := args["topic_desc"].(string)
				res, err := svc.Add(userID, siteURL, topicDesc)
				if err != nil {
					return "", err
				}
				if len(res.Candidates) > 0 {
					var b strings.Builder
					b.WriteString(fmt.Sprintf("该站点发现 %d 个订阅源,请让用户选择后再订阅:\n", len(res.Candidates)))
					for i, c := range res.Candidates {
						b.WriteString(fmt.Sprintf("%d. %s — %s\n", i+1, displayTitle(c.Title), c.FeedURL))
					}
					return b.String(), nil
				}
				sub := res.Subscription
				return fmt.Sprintf("已订阅:%s\nfeed 地址:%s\n主题描述:%s\n(订阅 id:%d,后续 remove/pause 需用此 id)",
					displayTitle(sub.Title), sub.FeedURL, orEmpty(sub.TopicDesc), sub.ID), nil

			case "list":
				subs, err := svc.List(userID)
				if err != nil {
					return "", err
				}
				if len(subs) == 0 {
					return "当前没有任何订阅。用户提到常看的信息源时,可以建议用 add 订阅。", nil
				}
				var b strings.Builder
				b.WriteString(fmt.Sprintf("共 %d 个订阅源:\n", len(subs)))
				for _, s := range subs {
					status := ""
					if s.Status == subdomain.StatusPaused {
						status = "(已暂停,选源时跳过)"
					}
					b.WriteString(fmt.Sprintf("- [#%d] %s — %s %s\n", s.ID, displayTitle(s.Title), orEmpty(s.TopicDesc), status))
				}
				return b.String(), nil

			case "remove", "pause", "resume":
				id, err := toolArgInt64(args["id"])
				if err != nil {
					return "", fmt.Errorf("%s 需要提供订阅 id(从 list 结果获取)", action)
				}
				switch action {
				case "remove":
					if err := svc.Remove(userID, id); err != nil {
						return "", err
					}
					return fmt.Sprintf("已退订(id:%d)", id), nil
				default:
					status := subdomain.StatusPaused
					if action == "resume" {
						status = subdomain.StatusActive
					}
					if err := svc.SetStatus(userID, id, status); err != nil {
						return "", err
					}
					verb := "已暂停"
					if action == "resume" {
						verb = "已恢复"
					}
					return fmt.Sprintf("%s(id:%d)", verb, id), nil
				}

			default:
				return "", fmt.Errorf("不支持的操作: %s(可选 add/list/remove/pause/resume)", action)
			}
		},
	}
}

// toolArgInt64 工具参数里的整数(LLM JSON 数字统一 float64)。
func toolArgInt64(v interface{}) (int64, error) {
	switch n := v.(type) {
	case float64:
		return int64(n), nil
	case string:
		return strconv.ParseInt(strings.TrimSpace(n), 10, 64)
	case int:
		return int64(n), nil
	case int64:
		return n, nil
	default:
		return 0, fmt.Errorf("无效的整数参数")
	}
}

func displayTitle(t string) string {
	if strings.TrimSpace(t) == "" {
		return "未命名订阅源"
	}
	return t
}

func orEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(无)"
	}
	return s
}
