package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability"
	"github.com/PuerkitoBio/goquery"
)

// ssrfPolicy 校验 URL 是否安全可访问(防 SSRF:服务器端请求伪造)。
//
// 拒绝:
//   - 非 http/https 协议(file://, gopher://, ftp:// ...)
//   - 解析到内网/保留 IP(127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16,
//     169.254.0.0/16 链路本地/云元数据, ::1, fc00::/7, fe80::/10)
//   - 主机名解析失败或解析到内网(LLM 可能被诱导抓内网/云元数据,安全红线)
//
// 用法:web_fetcher / web_reader 抓取前先调 validateURL。
func validateURL(rawURL string) error {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("无效的 URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("仅支持 HTTP/HTTPS 协议")
	}
	host := parsed.Hostname()
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}

	// 解析主机名到 IP,检查每个 IP 是否内网/保留
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("域名解析失败")
	}
	for _, ip := range ips {
		if isPrivateOrReserved(ip) {
			return fmt.Errorf("禁止访问内网或保留地址")
		}
	}
	return nil
}

// isPrivateOrReserved 判断 IP 是否内网/保留/链路本地(含云元数据 169.254.x.x)。
func isPrivateOrReserved(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// 云元数据地址 169.254.169.254 已被 IsLinkLocalUnicast 覆盖
	return false
}

// sanitizeFetchError 把抓取错误转成不泄露内部细节的友好文案(安全红线)。
// 超时/连接拒绝/SSRF/非 HTML 等统一友好化,不透传 IP/堆栈/内部地址。
func sanitizeFetchError(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline"):
		return "抓取超时,请稍后重试或换用 web_reader"
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host"):
		return "无法连接到目标地址"
	case strings.Contains(msg, "内网") || strings.Contains(msg, "保留地址") || strings.Contains(msg, "协议"):
		return err.Error() // SSRF/协议错误已是友好文案,直接返回
	case strings.Contains(msg, "非 html") || strings.Contains(msg, "content-type"):
		return "目标非 HTML 页面,无法提取正文"
	default:
		return "抓取失败"
	}
}

const (
	webFetchMaxBytes = 8000    // 正文截断长度,防 token 爆炸
	maxHTMLBytes     = 2 << 20 // 抓取 HTML 原始大小上限 2MB,防超大页面 OOM(readability 需全量 HTML)
)

// fetchAndExtract 抓取 URL 并提取正文(goquery 解析)。不含 SSRF 校验(由调用方先 validateURL)。
// 抽出独立函数便于单测(测试用 httptest 本地 server,会触发 SSRF 拒绝,故直接测此函数)。
// 失败统一 formatWebFailure(标记文本),成功 formatWebSuccess(头 + 裸正文)。
func fetchAndExtract(ctx context.Context, urlStr string) (string, error) {
	client := &http.Client{
		Timeout: 15 * time.Second,
		// P0 SSRF 防护:每次跳转重新校验目标。validateURL 只在 Execute 校验原始 URL,
		// 默认 http.Client 会跟 10 跳到任意主机,恶意页 302 -> 169.254.169.254 会把云元数据读进正文。
		// 这里只校验跳转目标(不校验初始 URL,否则 httptest 127.0.0.1 测试全挂)。
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("重定向次数过多")
			}
			return validateURL(req.URL.String())
		},
	}
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", formatWebFailure(0, urlStr, sanitizeFetchError(err))
	}
	req.Header.Set("User-Agent", "OmniBot/1.0 (web_read; +https://github.com/Archy-yang/omnibot)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", formatWebFailure(0, urlStr, sanitizeFetchError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 401/403:目标站点要鉴权;4xx/5xx:不可达。标记文本让模型据 code 决定是否放弃该站点。
		return "", formatWebFailure(resp.StatusCode, urlStr, http.StatusText(resp.StatusCode))
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		return "", formatWebFailure(resp.StatusCode, urlStr, "目标非 HTML 页面,无法提取正文")
	}

	// 读 body 到内存(readability 与 goquery 都要消费 reader,不能共享流);限 2MB 防超大 HTML OOM。
	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxHTMLBytes+1))
	if err != nil {
		return "", formatWebFailure(0, urlStr, sanitizeFetchError(err))
	}

	// P1:readability 优先(Readability.js 算法,正文鲁棒),失败/空则 goquery 兜底(非文章页/落地页)。
	title, text := extractArticle(bodyBytes, urlStr)

	if strings.TrimSpace(text) == "" {
		return "", formatWebFailure(0, urlStr, "抓取到空正文(可能是 JS 渲染页面,换用 web_reader)")
	}
	if len(text) > webFetchMaxBytes {
		text = text[:webFetchMaxBytes] + "\n...(正文过长已截断)"
	}

	result := text
	if title != "" {
		result = "标题: " + strings.TrimSpace(title) + "\n\n" + text
	}
	return formatWebSuccess(resp.StatusCode, urlStr, result), nil
}

// extractArticle 用 readability 抽正文,失败/空则回退 goquery(手写选择器)。
// readability 对标准文章页鲁棒;goquery 兜底非文章结构(落地页/列表页/特殊布局)。
func extractArticle(body []byte, pageURL string) (title, text string) {
	if t, txt, ok := extractWithReadability(body, pageURL); ok {
		return t, txt
	}
	return extractWithGoquery(body)
}

// extractWithReadability 用 go-readability(Readability.js 移植)抽正文。返回纯文本(TextContent)。
// 抽不到(ok=false)时由调用方回退 goquery。抽独立函数便于单测。
func extractWithReadability(body []byte, pageURL string) (title, text string, ok bool) {
	parsed, err := url.Parse(pageURL)
	if err != nil {
		return "", "", false
	}
	article, err := readability.FromReader(bytes.NewReader(body), parsed)
	if err != nil {
		return "", "", false
	}
	txt := strings.TrimSpace(article.TextContent)
	if txt == "" {
		return "", "", false
	}
	return strings.TrimSpace(article.Title), txt, true
}

// extractWithGoquery 用 goquery 手写选择器抽正文(去 script/style/nav 等噪音)。
// 作为 readability 的兜底:readability 抽不出正文的非文章页(落地页/列表页)走此路径。
func extractWithGoquery(body []byte) (title, text string) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", ""
	}
	doc.Find("script,style,nav,aside,header,footer,form,iframe,noscript,svg,button").Remove()
	selection := doc.Find("article, main, [role=main], .content, .article-body, .post-content").First()
	if selection.Length() == 0 {
		selection = doc.Find("body")
	}
	title = doc.Find("title").First().Text()
	text = cleanText(selection.Text())
	return title, text
}

// cleanText 压缩空白:多空格/换行合并,去首尾空白。
func cleanText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}
