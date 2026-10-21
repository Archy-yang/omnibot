package tools

import (
	"context"
	"errors"
	"fmt"
	"time"

	"omnibot/internal/pkg/toolcore"
)

// 档案同步到飞书文档(23 号 F2:对话内同步工具)。
// 包纪律同 subscription_tools:agent 包只依赖此窄接口,不 import service 实现
// (feishudoc.Service 由 wire 适配)。

// sentinel 错误:模型可读的 friendly 文案,由 wire 适配层从 feishudoc sentinel 翻译而来。
var (
	ErrMatterDocNotFound     = errors.New("档案不存在或不属于当前用户,可先用 search_matters 查到正确档案 ID")
	ErrMatterDocUnauthorized = errors.New("该用户尚未完成飞书授权,无法同步文档;请提示用户先在设置里完成飞书绑定")
)

// MatterDocSyncResult 同步结果(与 feishudoc.SyncResult 解耦)。
type MatterDocSyncResult struct {
	DocURL   string
	Created  bool
	SyncedAt time.Time
}

// MatterDocSyncer 档案→飞书文档同步窄接口。
type MatterDocSyncer interface {
	SyncMatter(ctx context.Context, userID, matterID int64) (MatterDocSyncResult, error)
}

// CreateMatterDocSyncTool 对话内档案同步:用户说"把 XX 档案同步到飞书"时直调。
// 首次创建文档,此后覆盖更新同一文档(23 号 §4:文档是导出镜像,覆盖前由用户确认的
// 交互放在前端按钮路径;对话内由模型复述"将覆盖更新"语义)。
func CreateMatterDocSyncTool(svc MatterDocSyncer) toolcore.Tool {
	return toolcore.Tool{
		Name:         "sync_matter_doc",
		Description:  "把一个个人档案(事项)的时间线同步为飞书文档。用户表达\"把XX档案/事项同步到飞书/导出成文档\"时使用。首次同步创建文档,再次同步覆盖更新同一文档(飞书侧手动编辑会丢失)。档案 ID 不确定时先用 search_matters 查询。",
		DisplayLabel: "同步了档案到飞书文档",
		Capabilities: []string{toolcore.CapMemory},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"matter_id": map[string]interface{}{
					"type":        "number",
					"description": "档案 ID(整数,先用 search_matters 查询确认)",
				},
			},
			"required": []string{"matter_id"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			id, ok := args["matter_id"].(float64)
			if !ok || id <= 0 || id != float64(int64(id)) {
				return "", fmt.Errorf("需要 matter_id(数字,先用 search_matters 查询)")
			}
			userID := toolcore.UserIDFromContext(ctx)
			result, err := svc.SyncMatter(ctx, userID, int64(id))
			if err != nil {
				return "", err // sentinel 已带模型可读文案;其余透传(CLI 失败等)
			}
			verb := "已更新"
			if result.Created {
				verb = "已创建"
			}
			return fmt.Sprintf("%s飞书文档:%s\n上次同步时间:%s", verb, result.DocURL,
				result.SyncedAt.Format("2006-01-02 15:04")), nil
		},
	}
}
