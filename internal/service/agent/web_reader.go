package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// jinaReaderBase Jina AI Reader 服务前缀。拼接目标 URL:https://r.jina.ai/<url>
	// Jina Reader 抓取目标页(含 JS 渲染),返回清洗后的 Markdown 正文,专为 LLM 设计。
	jinaReaderBase = "https://r.jina.ai/"
	webReaderMaxBytes = 12000 // Jina 质量高,允许更长正文
)

// CreateWebReaderTool 创建 web_reader 工具:用 Jina Reader 抓取复杂/JS 渲染页面。
//
// 与 web_fetcher 分工:
//   - web_fetcher:本地 goquery 解析,适合静态 HTML(快、零外部依赖)
//   - web_reader:Jina Reader 服务,适合 web_fetcher 搞不定的复杂页面(SPA、JS 渲染、
//     正文提取失败)。质量更高但有外部依赖(目标 URL 经 Jina 中转)。
//
// 模型应先用 web_fetcher,返回空/乱码/报错时再升级到 web_reader。
//
// 安全:SSRF 防护(拒内网),超时 30s,错误脱敏。
func CreateWebReaderTool() Tool {
	return Tool{
		Name:        "web_reader",
		DisplayLabel: "读取了网页(Jina)",
		Description: "通过 Jina Reader 服务抓取网页正文(返回 Markdown,支持 JS 渲染的复杂页面)。" +
			"当 web_fetcher 返回空正文、乱码或报错(JS 渲染页面/SPA)时换用此工具。" +
			"质量更高但较慢。传入完整 HTTP/HTTPS 链接。",
		Parameters: map[string]interface{}{
			"type": "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要抓取的网页完整 HTTP/HTTPS 链接(web_fetcher 搞不定时用)",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			urlStr, _ := args["url"].(string)
			if urlStr == "" {
				return "", fmt.Errorf("url 不能为空")
			}
			if err := validateURL(urlStr); err != nil {
				return "", err
			}
			return readViaJina(ctx, urlStr)
		},
	}
}

// readViaJina 调 Jina Reader 抓取。抽独立函数便于单测(mock server)。
func readViaJina(ctx context.Context, targetURL string) (string, error) {
	jinaURL := jinaReaderBase + targetURL

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", jinaURL, nil)
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}
	// Jina Reader 推荐带 Accept: text/markdown
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("User-Agent", "OmniBot/1.0 (web_reader)")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("读取服务返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webReaderMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}

	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", fmt.Errorf("读取到空正文")
	}
	if len(text) > webReaderMaxBytes {
		text = text[:webReaderMaxBytes] + "\n...(正文过长已截断)"
	}
	return text, nil
}
