package tools

// rss_reader.go — RSS 抓取工具(14-订阅源管理的查询执行器;自 agent 包迁入)。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mmcdole/gofeed"

	"omnibot/internal/pkg/toolcore"
)

func CreateRSSReaderTool() toolcore.Tool {
	return toolcore.Tool{
		Name:         "rss_reader",
		Description:  "解析并获取RSS/Atom订阅源的内容，支持所有主流RSS(0.9x/1.0/2.0)和Atom(0.3/1.0)格式。传入RSS链接，返回订阅源的基本信息和最新文章列表。",
		DisplayLabel: "读取了 RSS 订阅",
		Capabilities: []string{toolcore.CapResearch, toolcore.CapWeb, toolcore.CapIngest},
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要获取的RSS/Atom订阅源的完整HTTP/HTTPS链接，必须是公开可访问的RSS地址",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "返回的文章数量限制，默认返回最新10篇，最多不超过50篇",
					"default":     10,
					"minimum":     1,
					"maximum":     50,
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			// 解析参数
			urlStr, ok := args["url"].(string)
			if !ok || urlStr == "" {
				return "", fmt.Errorf("RSS链接不能为空")
			}

			limit := 10
			if limitVal, ok := args["limit"].(float64); ok {
				limit = int(limitVal)
				if limit <= 0 {
					limit = 10
				}
				if limit > 50 {
					limit = 50
				}
			}

			// 校验URL合法性
			parsedURL, err := url.Parse(urlStr)
			if err != nil {
				return "", fmt.Errorf("无效的URL格式: %w", err)
			}
			if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
				return "", fmt.Errorf("仅支持HTTP/HTTPS协议的RSS链接")
			}

			// 创建RSS解析器，设置10秒超时
			parser := gofeed.NewParser()
			parser.Client = &http.Client{
				Timeout: 10 * time.Second,
			}

			// 解析RSS内容
			feed, err := parser.ParseURL(urlStr)
			if err != nil {
				return "", fmt.Errorf("RSS解析失败: %w，请确认链接是有效的RSS/Atom订阅地址", err)
			}

			// 处理返回结果
			type RSSFeedItem struct {
				Title       string     `json:"title"`
				Link        string     `json:"link"`
				Description string     `json:"description,omitempty"`
				Content     string     `json:"content,omitempty"`
				PublishedAt *time.Time `json:"published_at,omitempty"`
				Author      string     `json:"author,omitempty"`
				Categories  []string   `json:"categories,omitempty"`
			}

			type RSSFeedResult struct {
				Title       string        `json:"title"`
				Description string        `json:"description"`
				Link        string        `json:"link"`
				UpdatedAt   *time.Time    `json:"updated_at,omitempty"`
				Items       []RSSFeedItem `json:"items"`
				TotalItems  int           `json:"total_items"`
				Returned    int           `json:"returned"`
			}

			result := RSSFeedResult{
				Title:       feed.Title,
				Description: feed.Description,
				Link:        feed.Link,
				UpdatedAt:   feed.UpdatedParsed,
				TotalItems:  len(feed.Items),
				Returned:    min(limit, len(feed.Items)),
			}

			// 截取指定数量的文章
			end := min(limit, len(feed.Items))
			for _, item := range feed.Items[:end] {
				feedItem := RSSFeedItem{
					Title:       item.Title,
					Link:        item.Link,
					Description: item.Description,
					Content:     item.Content,
					PublishedAt: item.PublishedParsed,
				}
				if item.Author != nil {
					feedItem.Author = item.Author.Name
				}
				feedItem.Categories = item.Categories
				result.Items = append(result.Items, feedItem)
			}

			// 序列化为JSON返回
			resultJSON, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				return "", fmt.Errorf("结果序列化失败: %w", err)
			}

			return string(resultJSON), nil
		},
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
