package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mmcdole/gofeed"
)

// MemoryProvider 记忆查询接口
type MemoryProvider interface {
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}

// CreateGetCurrentTimeTool 获取当前时间工具
func CreateGetCurrentTimeTool() Tool {
	return Tool{
		Name:        "get_current_time",
		Description: "获取当前的日期和时间",
		Parameters: map[string]interface{}{
			"type":       "object",
			"properties": map[string]interface{}{},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return time.Now().Format("2006-01-02 15:04:05 MST"), nil
		},
	}
}

// CreateCalculatorTool 计算器工具
func CreateCalculatorTool() Tool {
	return Tool{
		Name:        "calculator",
		Description: "执行安全的数学计算（仅支持四则运算和括号）",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"expression": map[string]interface{}{
					"type":        "string",
					"description": "数学表达式，如 \"2 + 3 * 4\"",
				},
			},
			"required": []string{"expression"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			expr, ok := args["expression"].(string)
			if !ok || expr == "" {
				return "", fmt.Errorf("expression is required")
			}
			result, err := safeEval(expr)
			if err != nil {
				return "", fmt.Errorf("计算失败: %w", err)
			}
			return strconv.FormatFloat(result, 'f', -1, 64), nil
		},
	}
}

// CreateSearchMemoriesTool 搜索记忆工具
func CreateSearchMemoriesTool(memorySvc MemoryProvider) Tool {
	return Tool{
		Name:        "search_memories",
		Description: "搜索用户的长期记忆，查找与查询相关的记忆内容",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索关键词",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			query, ok := args["query"].(string)
			if !ok || query == "" {
				return "", fmt.Errorf("query is required")
			}
			userID := getUserIDFromContext(ctx)
			memories, err := memorySvc.GetRecentForContext(ctx, userID, 50)
			if err != nil {
				return "", fmt.Errorf("查询记忆失败: %w", err)
			}
			return filterMemories(memories, query), nil
		},
	}
}

// CreateSearchHistoryTool 搜索对话历史工具
func CreateSearchHistoryTool() Tool {
	return Tool{
		Name:        "search_history",
		Description: "搜索用户的历史对话记录",
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"query": map[string]interface{}{
					"type":        "string",
					"description": "搜索关键词",
				},
				"limit": map[string]interface{}{
					"type":        "integer",
					"description": "返回条数限制，默认 5",
				},
			},
			"required": []string{"query"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			return "历史搜索功能将在后续版本中完善", nil
		},
	}
}

// safeEval 安全的数学表达式求值（仅允许数字、运算符、括号、空格和小数点）
func safeEval(expr string) (float64, error) {
	for _, ch := range expr {
		if !strings.ContainsRune("0123456789+-*/(). ", ch) {
			return 0, fmt.Errorf("表达式包含不允许的字符: %c", ch)
		}
	}
	node, err := parser.ParseExpr(expr)
	if err != nil {
		return 0, err
	}
	return evalNode(node)
}

func evalNode(node ast.Expr) (float64, error) {
	switch n := node.(type) {
	case *ast.BinaryExpr:
		left, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(n.Y)
		if err != nil {
			return 0, err
		}
		switch n.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, fmt.Errorf("division by zero")
			}
			return left / right, nil
		default:
			return 0, fmt.Errorf("unsupported operator: %s", n.Op)
		}
	case *ast.ParenExpr:
		return evalNode(n.X)
	case *ast.BasicLit:
		if n.Kind == token.INT || n.Kind == token.FLOAT {
			return strconv.ParseFloat(n.Value, 64)
		}
		return 0, fmt.Errorf("unsupported literal: %s", n.Value)
	case *ast.UnaryExpr:
		val, err := evalNode(n.X)
		if err != nil {
			return 0, err
		}
		if n.Op == token.SUB {
			return -val, nil
		}
		return val, nil
	default:
		return 0, fmt.Errorf("unsupported expression type: %T", node)
	}
}

func filterMemories(memories []string, query string) string {
	query = strings.ToLower(query)
	var matched []string
	for _, m := range memories {
		if strings.Contains(strings.ToLower(m), query) {
			matched = append(matched, m)
		}
	}
	if len(matched) == 0 {
		return fmt.Sprintf("未找到与 \"%s\" 相关的记忆", query)
	}
	return strings.Join(matched, "\n")
}

// CreateRSSReaderTool RSS订阅阅读工具，支持解析所有主流RSS/Atom格式
func CreateRSSReaderTool() Tool {
	return Tool{
		Name:        "rss_reader",
		Description: "解析并获取RSS/Atom订阅源的内容，支持所有主流RSS(0.9x/1.0/2.0)和Atom(0.3/1.0)格式。传入RSS链接，返回订阅源的基本信息和最新文章列表。",
		Parameters: map[string]interface{}{
			"type": "object",
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

type contextKey string

const userIDContextKey contextKey = "agent_user_id"

func withUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDContextKey, userID)
}

func getUserIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(userIDContextKey).(int64); ok {
		return id
	}
	return 0
}
