package subscription

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	subdomain "omnibot/internal/domain/subscription"
	"strings"
	"sync"
	"time"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

// Discoverer RSS 自动发现:网站/博客地址 → 可用 feed 地址。
// 确定性三步,无 LLM 参与:① head <link rel="alternate"> 声明 → ② 约定路径
// → ③ gofeed 真抓验证(能解析出条目才算数,防止把普通页面当 feed)。
type Discoverer interface {
	// Discover 返回全部验证通过的候选(≥1);0 条返回明确错误——宁漏勿错,不猜。
	Discover(siteURL string) ([]*subdomain.FeedInfo, error)
	// Validate 验证单个地址是否为可用 feed(用户直接给 feed 地址时走此捷径)。
	Validate(feedURL string) (*subdomain.FeedInfo, error)
}

type discoverer struct {
	client *http.Client
	parser *gofeed.Parser
}

// discoverTimeout 单次发现的整体预算:候选并行验证,最坏耗时 ≈ 两次请求超时而非
// 串行 N×超时(真实网络里不可达主机常见,串行会拖到分钟级,阻塞对话轮)。
const (
	discoverTimeout   = 30 * time.Second
	perRequestTimeout = 10 * time.Second
	maxParallelProbes = 8
	discoverUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/126.0 Safari/537.36"
)

func NewDiscoverer() Discoverer {
	client := &http.Client{
		Timeout: perRequestTimeout,
		// 浏览器 UA:不少站点对默认 Go/gofeed UA 直接 403/挑战
		Transport: uaTransport{base: http.DefaultTransport},
	}
	parser := gofeed.NewParser()
	parser.Client = client
	return &discoverer{client: client, parser: parser}
}

// uaTransport 给所有请求补浏览器 UA(gofeed 内部发起的请求同样生效)。
type uaTransport struct{ base http.RoundTripper }

func (u uaTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", discoverUserAgent)
	}
	return u.base.RoundTrip(req)
}

// fallbackPaths head 无声明时逐个尝试的约定路径(WordPress/Hugo/Ghost/Hexo 等)。
var fallbackPaths = []string{
	"/feed", "/feed/", "/rss", "/rss/", "/atom.xml", "/index.xml", "/feed.xml", "/rss.xml", "/?feed=rss2",
}

func (d *discoverer) Discover(siteURL string) ([]*subdomain.FeedInfo, error) {
	base, err := url.Parse(siteURL)
	if err != nil {
		return nil, fmt.Errorf("无效的网站地址: %w", err)
	}
	if base.Scheme != "http" && base.Scheme != "https" {
		return nil, fmt.Errorf("仅支持 HTTP/HTTPS 协议的网站地址")
	}
	if base.Host == "" {
		return nil, fmt.Errorf("网站地址缺少主机名")
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoverTimeout)
	defer cancel()

	// ① head 声明 → ③ 并行验证;全部不可用时追加 ② 约定路径再验证一轮。
	//    常见案例:站点声明托管在第三方(如 feedburner)但该域不可达,
	//    而站点本地仍有 /atom.xml——声明的候选全军覆没不等于没有源。
	validated, _ := d.validateCandidates(ctx, d.headCandidates(ctx, base))
	if len(validated) == 0 {
		validated, _ = d.validateCandidates(ctx, fallbackCandidates(base))
	}
	if len(validated) == 0 {
		return nil, fmt.Errorf("该站点未发现可用的 RSS/Atom 订阅源")
	}
	return validated, nil
}

// fallbackCandidates 约定路径候选:博客常挂在主机子路径下(如 ruanyifeng.com/blog/),
// 所以主机根与用户给定子路径各拼一遍。
func fallbackCandidates(base *url.URL) []string {
	roots := []string{""}
	if p := strings.TrimSuffix(base.Path, "/"); p != "" && p != "/" {
		roots = append(roots, p)
	}
	var out []string
	for _, root := range roots {
		for _, p := range fallbackPaths {
			out = append(out, base.Scheme+"://"+base.Host+root+p)
		}
	}
	return out
}

// validateCandidates 并行真抓验证,按候选顺序收集——并行把最坏耗时从 串行N×超时
// 压到 单次超时,配合整体 30s 预算保证发现流程有界(不可达主机是常态而非异常)。
func (d *discoverer) validateCandidates(ctx context.Context, candidates []string) ([]*subdomain.FeedInfo, []error) {
	if len(candidates) == 0 {
		return nil, nil
	}
	type hit struct {
		idx  int
		info *subdomain.FeedInfo
	}
	hits := make(chan hit, len(candidates))
	sem := make(chan struct{}, maxParallelProbes)
	var wg sync.WaitGroup
	for i, cand := range candidates {
		wg.Add(1)
		go func(idx int, feedURL string) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			info, err := d.validateCtx(ctx, feedURL)
			if err != nil {
				return // 非法/不可达候选过滤,不冒充结果
			}
			hits <- hit{idx: idx, info: info}
		}(i, cand)
	}
	wg.Wait()
	close(hits)

	byIdx := make(map[int]*subdomain.FeedInfo, len(hits))
	for h := range hits {
		byIdx[h.idx] = h.info
	}
	var validated []*subdomain.FeedInfo
	for i := 0; i < len(candidates); i++ {
		if info, ok := byIdx[i]; ok {
			validated = append(validated, info)
		}
	}
	return validated, nil
}

// headCandidates 抓取页面,解析 <link rel="alternate"> 的 feed 声明(href 相对路径按页面地址解析)。
// 页面抓不到时返回空(调用方继续走约定路径兜底)。
func (d *discoverer) headCandidates(ctx context.Context, base *url.URL) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return nil
	}
	resp, err := d.client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}

	doc, err := html.Parse(resp.Body)
	if err != nil {
		return nil
	}

	var candidates []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "link" {
			rel, typeAttr, href := "", "", ""
			for _, a := range n.Attr {
				switch strings.ToLower(a.Key) {
				case "rel":
					rel = strings.ToLower(a.Val)
				case "type":
					typeAttr = strings.ToLower(strings.TrimSpace(a.Val))
				case "href":
					href = a.Val
				}
			}
			if strings.Contains(rel, "alternate") && isFeedType(typeAttr) && href != "" {
				if ref, err := url.Parse(href); err == nil {
					candidates = append(candidates, base.ResolveReference(ref).String())
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	return candidates
}

// isFeedType 识别 feed MIME 声明(RSS/Atom/JSON Feed)。
func isFeedType(t string) bool {
	return t == "application/rss+xml" || t == "application/atom+xml" || t == "application/feed+json"
}

// Validate 真抓一次,能解析出 feed 且有条目才算数;Title/Description 取 feed 元数据。
func (d *discoverer) Validate(feedURL string) (*subdomain.FeedInfo, error) {
	return d.validateCtx(context.Background(), feedURL)
}

// validateCtx 真抓一次,能解析出 feed 且有条目才算数;Title/Description 取 feed 元数据。
func (d *discoverer) validateCtx(ctx context.Context, feedURL string) (*subdomain.FeedInfo, error) {
	feed, err := d.parser.ParseURLWithContext(feedURL, ctx)
	if err != nil || feed == nil || len(feed.Items) == 0 {
		return nil, fmt.Errorf("候选不可用: %s", feedURL)
	}
	return &subdomain.FeedInfo{
		FeedURL:     feedURL,
		Title:       strings.TrimSpace(feed.Title),
		Description: strings.TrimSpace(feed.Description),
	}, nil
}
