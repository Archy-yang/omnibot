package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 静态 HTML 页面(含噪音),web_fetcher 应提取正文去噪
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
	assert.Contains(t, result, "Go 1.24 新特性概览")
	assert.Contains(t, result, "泛型类型别名")
	// 噪音应被去除
	assert.NotContains(t, result, "var x=1")
	assert.NotContains(t, result, "color:red")
	assert.NotContains(t, result, "相关推荐")
	assert.NotContains(t, result, "版权所有")
}

func TestWebFetcher_EmptyArgs(t *testing.T) {
	tool := CreateWebFetcherTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
}

func TestWebFetcher_NonHTTPProtocol(t *testing.T) {
	tool := CreateWebFetcherTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "file:///etc/passwd",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP")
}

func TestWebFetcher_LoopbackSSRF(t *testing.T) {
	tool := CreateWebFetcherTool()
	// 127.0.0.1 是内网回环,应被 SSRF 防护拒绝
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://127.0.0.1:8080/api/v1/health",
	})
	require.Error(t, err)
	// httptest server 监听 127.0.0.1,但 validateURL 应在请求前拒绝
	assert.Contains(t, err.Error(), "内网")
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
