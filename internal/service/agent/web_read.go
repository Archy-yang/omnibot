package agent

import (
	"context"
	"fmt"
	"strings"
)

// web_read 是统一的网页抓取工具(P2),合并原 web_fetcher / web_reader。
//
// 核心改进:升级决策由程序而非 LLM 决定。mode=auto(默认)先本地解析(readability+goquery),
// 正文不足或失败时自动升级到 Jina JS 渲染服务。LLM 不再需要判断页面类型或手动换工具,
// 从根上抑制 task15/16/17 那类"fetcher 失败->换 URL->再 fetcher"的循环。
//
// mode:
//   - auto(默认):先 fetchAndExtract,needUpgrade 或 shouldTryJinaOnFailure 时自动升 readViaJina
//   - http:仅本地解析(快,静态页)
//   - reader:仅 Jina JS 渲染(慢,强制渲染,已知 SPA)
//
// 安全:validateURL(SSRF 防护,拒内网/保留地址)在 Execute 入口校验一次,
// fetcher 与 Jina 共享同一目标故不重复校验。fetcher 的 redirect 再校验见 web_fetcher.go。
const minContentForUpgrade = 500 // 正文短于此阈值(约 250 汉字)视为疑似导航/落地页,auto 升级 Jina

// CreateWebReadTool 创建 web_read 工具。
func CreateWebReadTool() Tool {
	return Tool{
		Name:         "web_read",
		DisplayLabel: "读取了网页",
		Capabilities: []string{CapResearch, CapWeb},
		Description: "抓取指定 URL 的网页正文。mode=auto(默认)自动选最快方式:先本地解析," +
			"正文不足或失败时自动升级到 JS 渲染服务,无需判断页面类型。mode=http 仅本地解析(快);" +
			"mode=reader 强制 JS 渲染(慢,已知 SPA)。优先用 auto。传入完整 HTTP/HTTPS 链接。",
		Parameters: map[string]interface{}{
			"type":     "object",
			"required": []string{"url"},
			"properties": map[string]interface{}{
				"url": map[string]interface{}{
					"type":        "string",
					"description": "要抓取的网页完整 HTTP/HTTPS 链接,必须是公开可访问的页面",
				},
				"mode": map[string]interface{}{
					"type":        "string",
					"enum":        []string{"auto", "http", "reader"},
					"description": "抓取方式:auto(默认,自动升级)/http(仅本地解析)/reader(仅 JS 渲染)",
				},
			},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			urlStr, _ := args["url"].(string)
			if urlStr == "" {
				return "", fmt.Errorf("url 不能为空")
			}
			mode, _ := args["mode"].(string)
			if mode == "" {
				mode = "auto"
			}
			// SSRF 防护:任一 mode 都先校验目标(fetcher 与 Jina 共享同一目标,校验一次)
			if err := validateURL(urlStr); err != nil {
				return "", formatWebFailure(0, urlStr, err.Error())
			}

			switch mode {
			case "http":
				return fetchAndExtract(ctx, urlStr)
			case "reader":
				return readViaJina(ctx, urlStr)
			default: // auto
				return webReadAuto(ctx, urlStr, fetchAndExtract, readViaJina)
			}
		},
	}
}

// webReadAuto 执行 auto 编排:fetcher 优先,必要时升级 Jina。
//
// 依赖注入 fetch/jina(签名同 fetchAndExtract/readViaJina)便于单测 mock。
// 编排规则:
//   - fetcher 成功 + 正文够长 -> 返回 fetcher 结果(不调 Jina)
//   - fetcher 成功 + 正文短(needUpgrade) -> 升级 Jina;Jina 失败退回 fetcher 结果
//   - fetcher 失败 + 可升级(shouldTryJinaOnFailure) -> 升级 Jina;Jina 失败返回 fetcher 错误
//   - fetcher 失败 + 不可升级(SSRF/协议/非HTML) -> 直接返回 fetcher 错误(不调 Jina)
func webReadAuto(ctx context.Context, urlStr string, fetch, jina func(context.Context, string) (string, error)) (string, error) {
	result, err := fetch(ctx, urlStr)
	if err == nil {
		if needUpgrade(result) {
			if jr, je := jina(ctx, urlStr); je == nil {
				return jr, nil
			}
			return result, nil // Jina 失败退回 fetcher 结果(有内容总比无好)
		}
		return result, nil
	}
	if shouldTryJinaOnFailure(err) {
		if jr, je := jina(ctx, urlStr); je == nil {
			return jr, nil
		}
		return "", err // Jina 也失败,返回 fetcher 错误
	}
	return "", err // SSRF/协议/非HTML:不该升级,直接返回 fetcher 错误
}

// needUpgrade 判定 fetcher 成功结果是否需升级 Jina。
// 输入是 formatWebSuccess 输出(含标记头+正文):提取 "正文:" 之后的文本,
// trim 后长度 < minContentForUpgrade 视为疑似导航/落地页,应升级 Jina 看 JS 渲染后是否有更多内容。
func needUpgrade(result string) bool {
	idx := strings.Index(result, "正文:")
	if idx < 0 {
		return true // 无正文标记,视为异常,升级
	}
	body := strings.TrimSpace(result[idx+len("正文:"):])
	return len(body) < minContentForUpgrade
}

// shouldTryJinaOnFailure 判定 fetcher 失败后是否值得升级 Jina。
// 输入 err 是 formatWebFailure 标记文本(我们自己控制格式)。
// 不升级(false):SSRF/协议/非HTML -- 目标 Jina 也搞不定或不应抓(Jina 中转但目标可疑)。
// 升级(true):超时/连接/403/429/空正文等 -- Jina 路径或 IP 不同可能成功;空正文多半是 JS 渲染页正是 Jina 强项。
//
// 注意:依赖 formatWebFailure 的标记文本关键词,若标记文本改格式需同步此函数。见 web_result.go。
func shouldTryJinaOnFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, kw := range []string{"内网", "保留地址", "仅支持", "协议", "非 HTML"} {
		if strings.Contains(msg, kw) {
			return false
		}
	}
	return true
}
