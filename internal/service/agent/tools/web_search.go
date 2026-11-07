package tools

// web_search.go — 联网搜索工具(32-联网搜索技术方案 §4.4)。
//
// 与 web_read 的分工:web_search 只"找线索"(标题/链接/摘要,不下载页面),
// 读正文仍走 web_read。这样一次搜索的 token 与延迟都可控。
//
// 可见性:主 Agent 可见(与 weather 同类——单点结构化查询,一次调用即答)。
// 执行期按 ctx 里的用户 id 取该用户的三方配置;未配置时返回可行动文案,不报错。

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"omnibot/internal/pkg/toolcore"
	searchsvc "omnibot/internal/service/search"
)

// Searcher 检索能力的窄接口(由 service/search 实现;测试用桩)。
type Searcher interface {
	Search(ctx context.Context, userID int64, query string, count int) (string, error)
}

// CreateWebSearchTool 创建 web_search 工具。
func CreateWebSearchTool(searcher Searcher) toolcore.Tool {
	return toolcore.Tool{
		Name:         "web_search",
		DisplayLabel: "检索了网络",
		Capabilities: []string{toolcore.CapResearch, toolcore.CapWeb},
		Description: "用搜索引擎检索网页,返回标题、链接与摘要。适合「有哪些相关页面 / 最近发生了什么」" +
			"这类还不确定该读哪个页面的时候;需要读某页正文时用 web_read,不要用本工具反复换措辞重试。" +
			"query 支持 site:example.com、after:2026-01-01、lang:zh、\"精确短语\"、-排除词 等操作符。",
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"query"},
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "检索词,1~128 字;支持 site:/after:/lang:/\"短语\"/-排除 等操作符",
				},
				"count": map[string]interface{}{
					"type":        "integer",
					"description": "返回条数 1~20,留空用用户配置的默认值",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, _ := args["query"].(string)
			query = strings.TrimSpace(query)
			if query == "" {
				return "", fmt.Errorf("query 不能为空")
			}

			userID := toolcore.UserIDFromContext(ctx)
			if userID == 0 {
				// 身份缺失等价于"没有可用配置":不让它变成一次匿名检索(计费落在谁头上说不清)。
				return searchsvc.UserMessage(searchsvc.ErrNotConfigured), nil
			}

			out, err := searcher.Search(ctx, userID, query, searchCountArg(args["count"]))
			if err == nil {
				return out, nil
			}

			// 参数错误要让模型看见并自我纠正;其余(未配置/密钥/限流/超时)
			// 转成可行动文案返回——"搜不到/用不了"不是工具故障,不该计入熔断。
			if errors.Is(err, searchsvc.ErrInvalidQuery) {
				return "", fmt.Errorf("%s", searchsvc.UserMessage(err))
			}
			return searchsvc.UserMessage(err), nil
		},
	}
}

// searchCountArg 解析可选 count(JSON 数字到 Go 是 float64)。
// 非法/缺省返回 0,由 service 回落到用户配置值。
func searchCountArg(v interface{}) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case int64:
		return int(n)
	default:
		return 0
	}
}
