package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 静态 HTML 页面(含噪音),web_read(mode=http) 底层 fetchAndExtract 应提取正文去噪
const sampleHTML = `<!DOCTYPE html><html><head><title>Go 1.24 新特性</title>
<script>var x=1;</script><style>body{color:red}</style></head>
<body>
<nav>首页 文档 社区</nav>
<header>OmniBot</header>
<article>
<h1>Go 1.24 新特性概览</h1>
<p>Go 1.24 引入了泛型类型别名,简化了类型定义。</p>
<p>此外,标准库新增了若干实用包,性能进一步提升。</p>
</article>
<aside>相关推荐</aside>
<footer>版权所有</footer>
</body></html>`

func TestWebFetcher_ExtractsArticle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(sampleHTML))
	}))
	defer server.Close()

	result, err := fetchAndExtract(context.Background(), server.URL)
	require.NoError(t, err)
	// 新格式:成功也带标记头([抓取成功] HTTP 200 + URL + 正文),让模型读到结构化状态
	assert.Contains(t, result, "[抓取成功]")
	assert.Contains(t, result, "HTTP 200")
	assert.Contains(t, result, "正文:")
	// readability 用 <title> 作标题,正文为 article 内 <p> 文本(首个 <h1> 视为标题级元素不并入正文)
	assert.Contains(t, result, "标题: Go 1.24 新特性")
	assert.Contains(t, result, "泛型类型别名")
	// 噪音应被去除
	assert.NotContains(t, result, "var x=1")
	assert.NotContains(t, result, "color:red")
	assert.NotContains(t, result, "相关推荐")
	assert.NotContains(t, result, "版权所有")
}

func TestWebFetcher_NonHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer server.Close()

	result, err := fetchAndExtract(context.Background(), server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "非 HTML")
	_ = result
}

func TestValidateURL_RejectsPrivate(t *testing.T) {
	tests := []string{
		"http://127.0.0.1/",
		"http://10.0.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/", // 云元数据
		"http://172.16.0.1/",
		"file:///etc/passwd",
		"ftp://example.com/",
	}
	for _, u := range tests {
		err := validateURL(u)
		assert.Error(t, err, "应拒绝 %s", u)
	}
}

func TestValidateURL_RejectsBadScheme(t *testing.T) {
	err := validateURL("javascript:alert(1)")
	assert.Error(t, err)
}

func TestSanitizeFetchError(t *testing.T) {
	assert.Contains(t, sanitizeFetchError(errTimeout("timeout")), "超时")
	assert.Contains(t, sanitizeFetchError(errTimeout("context deadline exceeded")), "超时")
	assert.Contains(t, sanitizeFetchError(errTimeout("connection refused")), "无法连接")
	assert.Equal(t, "抓取失败", sanitizeFetchError(errTimeout("some other error")))
}

type errTimeout string

func (e errTimeout) Error() string { return string(e) }

// TestWebFetcher_RedirectToMetadataBlocked 验证 P0:跳转到云元数据地址应被 CheckRedirect 拦截。
// 现状(http.Client 默认跟 10 跳)会把元数据读进正文喂给 LLM -> SSRF 绕过。
// 用带超时的 ctx 兜底,避免 red 阶段连不上 169.254.x 时拖到 15s client 超时。
// TestExtractWithReadability_Article readability 应从标准文章页抽出正文(Readability.js 算法,去噪)。
func TestExtractWithReadability_Article(t *testing.T) {
	title, text, ok := extractWithReadability([]byte(sampleHTML), "https://example.com/go124")
	require.True(t, ok)
	assert.Contains(t, text, "泛型类型别名")
	// readability 用 <title> 作标题;正文为 article 内 <p>(首个 <h1> 视为标题级,不并入正文)
	assert.Contains(t, title, "Go 1.24 新特性")
	assert.NotContains(t, text, "var x=1")
	assert.NotContains(t, text, "版权所有")
}

// TestExtractWithGoquery_Article goquery 兜底路径应抽出正文。
// 验证兜底函数自身正确(独立于 readability);readability 失败/空时由 extractArticle 回退到此。
func TestExtractWithGoquery_Article(t *testing.T) {
	title, text := extractWithGoquery([]byte(sampleHTML))
	assert.Contains(t, text, "泛型类型别名")
	// goquery 保留 article 内 <h1>(与 readability 差异:readability 将 h1 视为标题不并入正文)
	assert.Contains(t, text, "Go 1.24 新特性概览")
	assert.Contains(t, title, "Go 1.24 新特性")
	assert.NotContains(t, text, "var x=1")
	assert.NotContains(t, text, "版权所有")
}

// TestExtractArticle_FallsBackWhenReadabilityEmpty readability 抽不出正文时,extractArticle 应回退 goquery 且不 panic。
// 注:readability 实测很宽容(nav/form/列表页/垃圾输入都能抽),仅空页才 ok=false。
// 此用例证明降级分支可执行不报错;goquery 兜底正确性由 TestExtractWithGoquery_Article 覆盖。
func TestExtractArticle_FallsBackWhenReadabilityEmpty(t *testing.T) {
	title, text := extractArticle([]byte(`<html><head></head><body></body></html>`), "https://example.com/empty")
	assert.Empty(t, title)
	assert.Empty(t, text)
}

func TestWebFetcher_RedirectToMetadataBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := fetchAndExtract(ctx, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内网")
}

// TestWebFetcher_RedirectToLoopbackBlocked 验证 P0:跳转到回环地址应被拦截。
// httptest server 本身监听 127.0.0.1,但 CheckRedirect 只校验跳转目标(初始 URL 由 Execute.validateURL 把关),
// 故初始请求不被拦;302 到 127.0.0.1:9 触发 CheckRedirect -> 拒绝。
func TestWebFetcher_RedirectToLoopbackBlocked(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:9/internal", http.StatusFound)
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err := fetchAndExtract(ctx, server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内网")
}
