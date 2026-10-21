package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"omnibot/internal/pkg/toolcore"
)

// 档案/待办收口(28号 N1,方案 §3.2):对话内承接"这事结了/收掉吧"。
// 红线:收口只能由用户对话确认触发——工具是唯一通道,系统侧严禁调用。
// 包纪律同 subscription_tools:窄接口 + 自有 sentinel,wire 适配 service 实现。

// ErrMatterLifecycleNotFound 档案未命中(不存在/他人);wire 从 memory.ErrMatterNotFound 翻译。
var ErrMatterLifecycleNotFound = errors.New("档案不存在或不属于当前用户,可先用 search_matters 查到正确档案 ID")

// MatterSettleResult 收口结果(与 memory.MatterSettleResult 解耦)。
type MatterSettleResult struct {
	Title         string
	Status        string
	CascadedLoops int
}

// MatterLifecycleManager 档案/待办生命周期窄接口(memoryService 实现)。
type MatterLifecycleManager interface {
	// SettleMatter 档案状态转移:close(完结,级联关其下 open loops)/archive/reopen。
	SettleMatter(ctx context.Context, userID, matterID int64, action string) (MatterSettleResult, error)
	// CloseLoopByQuery 按关键词收口 open loop:唯一命中 closed=true;多命中返回候选;
	// 零命中空列表。
	CloseLoopByQuery(ctx context.Context, userID int64, query string) (candidates []string, closed bool, err error)
}

// CreateManageMatterTool 档案状态转移工具。
func CreateManageMatterTool(svc MatterLifecycleManager) toolcore.Tool {
	return toolcore.Tool{
		Name: "manage_matter",
		Description: "管理个人档案(事项)的生命周期。用户表达\"这事结了/搞定了/完成了吧/别跟进X了\"时," +
			"先用 search_matters 找到对应档案 ID 再调用:close=完结(其下未决待办一并关闭);" +
			"archive=搁置不再跟进;reopen=重新打开。拿不准用户是否真要收口时,先复述确认再调用。",
		DisplayLabel: "更新了档案状态",
		Capabilities: []string{toolcore.CapMemory},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"matter_id": map[string]interface{}{
					"type":        "number",
					"description": "档案 ID(先用 search_matters 查询确认)",
				},
				"action": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"close", "archive", "reopen"},
					"description": "close=完结(级联关闭其下待办);archive=搁置;reopen=重开",
				},
			},
			"required": []string{"matter_id", "action"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, ok := args["matter_id"].(float64)
			if !ok || id <= 0 {
				return "", fmt.Errorf("需要 matter_id(先用 search_matters 查询)")
			}
			action, _ := args["action"].(string)
			if action != "close" && action != "archive" && action != "reopen" {
				return "", fmt.Errorf("action 必须是 close/archive/reopen")
			}
			userID := toolcore.UserIDFromContext(ctx)
			res, err := svc.SettleMatter(ctx, userID, int64(id), action)
			if err != nil {
				return "", err // sentinel 已带模型可读文案;其余透传
			}
			var b strings.Builder
			switch action {
			case "close":
				b.WriteString(fmt.Sprintf("已完结「%s」", res.Title))
				if res.CascadedLoops > 0 {
					fmt.Fprintf(&b, ",联动关闭了 %d 条待办", res.CascadedLoops)
				}
				b.WriteString(";如需恢复,说\"重新打开\"或在管理面操作")
			case "archive":
				fmt.Fprintf(&b, "已归档「%s」,不再跟进;说\"重新打开\"可恢复", res.Title)
			case "reopen":
				fmt.Fprintf(&b, "已重新打开「%s」", res.Title)
			}
			return b.String(), nil
		},
	}
}

// CreateCloseLoopTool 待办(loop)收口工具——孤儿待办(未挂档案)的唯一对话内出口。
func CreateCloseLoopTool(svc MatterLifecycleManager) toolcore.Tool {
	return toolcore.Tool{
		Name: "close_loop",
		Description: "关闭一条挂起的待办(用户说\"这事办完了/不用查了/这条待办可以关了\")。" +
			"用待办内容里的关键词匹配;匹配到多条时会列出候选,请用户确认后换更具体的关键词重试。",
		DisplayLabel: "关闭了待办",
		Capabilities: []string{toolcore.CapMemory},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"loop_query": map[string]interface{}{
					"type":        "string",
					"description": "待办内容中的独特关键词(如\"复诊\"\"AI冷战查证\")",
				},
			},
			"required": []string{"loop_query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["loop_query"].(string)
			if strings.TrimSpace(query) == "" {
				return "", fmt.Errorf("需要 loop_query(待办内容中的关键词)")
			}
			userID := toolcore.UserIDFromContext(ctx)
			cands, closed, err := svc.CloseLoopByQuery(ctx, userID, query)
			if err != nil {
				return "", err
			}
			switch {
			case closed:
				return fmt.Sprintf("已收口该待办;说\"重新打开\"或在管理面可恢复。"), nil
			case len(cands) > 1:
				return "匹配到多条待办,请用户确认要关哪一条,再用更具体的关键词调用:\n- " +
					strings.Join(cands, "\n- "), nil
			default:
				return "没有找到匹配的挂起待办(只匹配未关闭状态);可换关键词重试,或确认它是否已被关闭。", nil
			}
		},
	}
}
