package search

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domain "omnibot/internal/domain/search"
)

// N4 provider:完整覆盖 DeepSeek Search 契约的请求形状与错误码映射。

func testConfigFor(serverURL string) *domain.Config {
	return &domain.Config{
		Provider:       domain.ProviderDeepSeek,
		BaseURL:        serverURL,
		APIKey:         "cipher",
		Enabled:        true,
		DefaultCount:   5,
		TimeoutSeconds: 5,
	}
}

func TestDeepSeekProvider_Success(t *testing.T) {
	var gotBody map[string]interface{}
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1/tools/search", r.URL.Path)
		assert.Equal(t, http.MethodPost, r.Method)
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
			{"title":"标题A","url":"https://a.example.com","summary":"摘要A","site_name":"站点A",
			 "date":"2026-06-01T00:00:00+00:00","rerank_score":2.4,"authority":3}
		]}`))
	}))
	defer srv.Close()

	p := NewDeepSeekProvider()
	results, err := p.Search(context.Background(), testConfigFor(srv.URL), "plain-key",
		Request{Query: "golang 泛型", Count: 3, NeedContent: false})
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "标题A", results[0].Title)
	assert.Equal(t, "https://a.example.com", results[0].URL)
	assert.Equal(t, "站点A", results[0].SiteName)
	assert.InDelta(t, 2.4, results[0].RerankScore, 0.001)

	assert.Equal(t, "Bearer plain-key", gotAuth)
	assert.Equal(t, "golang 泛型", gotBody["query"])
	assert.Equal(t, float64(3), gotBody["count"])
	assert.Equal(t, false, gotBody["need_content"])
}

func TestDeepSeekProvider_EmptyResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	results, err := NewDeepSeekProvider().Search(context.Background(), testConfigFor(srv.URL), "k",
		Request{Query: "冷门查询", Count: 5})
	require.NoError(t, err)
	assert.Empty(t, results)
}

func TestDeepSeekProvider_StatusErrors(t *testing.T) {
	for _, code := range []int{400, 401, 402, 403, 408, 413, 429, 500, 504} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(code)
				_, _ = w.Write([]byte(`{"error":{"message":"boom","type":"invalid_request_error"}}`))
			}))
			defer srv.Close()

			_, err := NewDeepSeekProvider().Search(context.Background(), testConfigFor(srv.URL), "k",
				Request{Query: "q", Count: 5})
			require.Error(t, err)

			var se *StatusError
			require.ErrorAs(t, err, &se)
			assert.Equal(t, code, se.Code)
		})
	}
}

func TestDeepSeekProvider_MalformedJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json at all`))
	}))
	defer srv.Close()

	_, err := NewDeepSeekProvider().Search(context.Background(), testConfigFor(srv.URL), "k",
		Request{Query: "q", Count: 5})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "not json at all", "错误信息不透传原始响应体")
}

func TestDeepSeekProvider_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1500 * time.Millisecond)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	cfg := testConfigFor(srv.URL)
	cfg.TimeoutSeconds = 1 // 秒级下限由 domain 管(5s),测试直接构造以缩短耗时

	_, err := NewDeepSeekProvider().Search(context.Background(), cfg, "k",
		Request{Query: "q", Count: 5})
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

// 配置了自定义 base_url(代理)时,端点应拼在其后。
func TestDeepSeekProvider_CustomBaseURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	cfg := testConfigFor(srv.URL + "/proxy/")
	_, err := NewDeepSeekProvider().Search(context.Background(), cfg, "k", Request{Query: "q", Count: 5})
	require.NoError(t, err)
	assert.Equal(t, "/proxy/v1/tools/search", gotPath)
}
