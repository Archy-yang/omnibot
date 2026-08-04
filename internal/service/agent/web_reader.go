package agent

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// jinaReaderBase Jina AI Reader 服务前缀。拼接目标 URL:https://r.jina.ai/<url>
	// Jina Reader 抓取目标页(含 JS 渲染),返回清洗后的 Markdown 正文,专为 LLM 设计。
	jinaReaderBase    = "https://r.jina.ai/"
	webReaderMaxBytes = 12000 // Jina 质量高,允许更长正文
)

// readViaJina 调 Jina Reader 抓取。抽独立函数便于单测(mock server)。
// 失败统一 formatWebFailure(标记文本),成功 formatWebSuccess(头 + 裸 Markdown)。
// 由 web_read(mode=reader 或 auto 升级)调用,不直接作为 Tool 暴露给 LLM。
func readViaJina(ctx context.Context, targetURL string) (string, error) {
	jinaURL := jinaReaderBase + targetURL

	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", jinaURL, nil)
	if err != nil {
		return "", formatWebFailure(0, targetURL, sanitizeFetchError(err))
	}
	// Jina Reader 推荐带 Accept: text/markdown
	req.Header.Set("Accept", "text/markdown")
	req.Header.Set("User-Agent", "OmniBot/1.0 (web_reader)")

	resp, err := client.Do(req)
	if err != nil {
		return "", formatWebFailure(0, targetURL, sanitizeFetchError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 401/403:Jina 报目标站点要鉴权;4xx/5xx:目标不可达或 Jina 失败。
		// 统一标记文本,让模型据 HTTP code 决定是否放弃该站点。
		return "", formatWebFailure(resp.StatusCode, targetURL, http.StatusText(resp.StatusCode))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webReaderMaxBytes+1))
	if err != nil {
		return "", formatWebFailure(0, targetURL, sanitizeFetchError(err))
	}

	text := strings.TrimSpace(string(body))
	if text == "" {
		return "", formatWebFailure(0, targetURL, "读取到空正文(可能是 JS 渲染页面或反爬)")
	}
	if len(text) > webReaderMaxBytes {
		text = text[:webReaderMaxBytes] + "\n...(正文过长已截断)"
	}
	return formatWebSuccess(resp.StatusCode, targetURL, text), nil
}
