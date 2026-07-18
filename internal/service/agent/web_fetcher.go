package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

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

const webFetchMaxBytes = 8000 // 正文截断长度,防 token 爆炸

// CreateWebFetcherTool 创建 web_fetcher 工具:用 goquery 抓取网页正文(本地解析)。
//
// 适合静态 HTML 页面(博客/文档/新闻):去 script/style/nav 等噪音,提取正文文本。
// 对需 JS 渲染的 SPA 页面(返回空或乱码)应换用 web_reader。
//
// 安全:SSRF 防护(拒内网/保留地址),超时 15s,错误脱敏。
func CreateWebFetcherTool() Tool {
	return Tool{
		Name:        "web_fetcher",
		DisplayLabel: "抓取了网页",
		Description: "抓取指定 URL 的网页正文(本地解析,去导航/脚本/样式,返回纯文本)。" +
			"适合静态 HTML 页面(博客、文档、新闻)。对需 JS 渲染的复杂页面(SPA)可能返回空或乱码," +
			"此时换用 web_reader。传入完整 HTTP/HTTPS 链接。",
		Parameters: map[string]interface{}{
			"type": "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要抓取的网页完整 HTTP/HTTPS 链接,必须是公开可访问的页面",
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
			return fetchAndExtract(ctx, urlStr)
		},
	}
}

// fetchAndExtract 抓取 URL 并提取正文(goquery 解析)。不含 SSRF 校验(由调用方先 validateURL)。
// 抽出独立函数便于单测(测试用 httptest 本地 server,会触发 SSRF 拒绝,故直接测此函数)。
func fetchAndExtract(ctx context.Context, urlStr string) (string, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}
	req.Header.Set("User-Agent", "OmniBot/1.0 (web_fetcher; +https://github.com/Archy-yang/omnibot)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("目标返回 HTTP %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "text/html") && !strings.Contains(ct, "application/xhtml") {
		return "", fmt.Errorf("非 HTML 页面,无法提取正文")
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return "", fmt.Errorf("%s", sanitizeFetchError(err))
	}

	doc.Find("script,style,nav,aside,header,footer,form,iframe,noscript,svg,button").Remove()
	selection := doc.Find("article, main, [role=main], .content, .article-body, .post-content").First()
	if selection.Length() == 0 {
		selection = doc.Find("body")
	}

	title := doc.Find("title").First().Text()
	text := cleanText(selection.Text())

	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("抓取到空正文(可能是 JS 渲染页面,换用 web_reader)")
	}
	if len(text) > webFetchMaxBytes {
		text = text[:webFetchMaxBytes] + "\n...(正文过长已截断)"
	}

	result := text
	if title != "" {
		result = "标题: " + strings.TrimSpace(title) + "\n\n" + text
	}
	return result, nil
}

// cleanText 压缩空白:多空格/换行合并,去首尾空白。
func cleanText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}
