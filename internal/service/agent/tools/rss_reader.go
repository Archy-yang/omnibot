package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/mmcdole/gofeed"
)

// RSSReaderTool RSS订阅阅读工具，支持解析所有主流RSS/Atom格式
type RSSReaderTool struct{}

// RSSReaderParams 工具参数
type RSSReaderParams struct {
	URL   string `json:"url"`
	Limit int    `json:"limit,omitempty"`
}

// RSSFeedResult 解析后的RSS结果
type RSSFeedResult struct {
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Link        string        `json:"link"`
	UpdatedAt   *time.Time    `json:"updated_at,omitempty"`
	Items       []RSSFeedItem `json:"items"`
	TotalItems  int           `json:"total_items"`
	Returned    int           `json:"returned"`
}

// RSSFeedItem RSS文章项
type RSSFeedItem struct {
	Title       string     `json:"title"`
	Link        string     `json:"link"`
	Description string     `json:"description,omitempty"`
	Content     string     `json:"content,omitempty"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	Author      string     `json:"author,omitempty"`
	Categories  []string   `json:"categories,omitempty"`
}

func NewRSSReaderTool() *RSSReaderTool {
	return &RSSReaderTool{}
}

func (t *RSSReaderTool) Name() string {
	return "rss_reader"
}

func (t *RSSReaderTool) Description() string {
	return "解析并获取RSS/Atom订阅源的内容，支持所有主流RSS(0.9x/1.0/2.0)和Atom(0.3/1.0)格式。传入RSS链接，返回订阅源的基本信息和最新文章列表。"
}

func (t *RSSReaderTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["url"],
		"properties": {
			"url": {
				"type": "string",
				"description": "要获取的RSS/Atom订阅源的完整HTTP/HTTPS链接，必须是公开可访问的RSS地址"
			},
			"limit": {
				"type": "integer",
				"description": "返回的文章数量限制，默认返回最新10篇，最多不超过50篇",
				"default": 10,
				"minimum": 1,
				"maximum": 50
			}
		}
	}`)
}

func (t *RSSReaderTool) Execute(ctx context.Context, params []byte) (string, error) {
	var req RSSReaderParams
	if err := json.Unmarshal(params, &req); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	// 校验URL合法性
	if req.URL == "" {
		return "", fmt.Errorf("RSS链接不能为空")
	}
	parsedURL, err := url.Parse(req.URL)
	if err != nil {
		return "", fmt.Errorf("无效的URL格式: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("仅支持HTTP/HTTPS协议的RSS链接")
	}

	// 设置默认limit
	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	// 创建RSS解析器，设置10秒超时
	parser := gofeed.NewParser()
	parser.Client = &http.Client{
		Timeout: 10 * time.Second,
	}

	// 解析RSS内容
	feed, err := parser.ParseURL(req.URL)
	if err != nil {
		return "", fmt.Errorf("RSS解析失败: %w，请确认链接是有效的RSS/Atom订阅地址", err)
	}

	// 处理返回结果
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
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
