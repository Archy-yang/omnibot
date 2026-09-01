package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// EmbeddingProvider 契约与实现测试(12-记忆系统技术方案 §5 / TDD#2)。
// 全部走 httptest 假服务,无外网依赖。

// TestOpenAICompatEmbedding 兼容端点(千帆v2/DashScope/OpenAI):路径、鉴权头、请求体、乱序 index 排序。
func TestOpenAICompatEmbedding(t *testing.T) {
	var gotPath, gotAuth, gotModel string
	var gotInputCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		gotInputCount = len(req.Input)
		// index 乱序返回,验证按 index 重排
		_, _ = w.Write([]byte(`{"data":[
			{"index":1,"embedding":[0.4,0.5]},
			{"index":0,"embedding":[0.1,0.2]}
		]}`))
	}))
	defer srv.Close()

	p, err := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: "openai_compatible", BaseURL: srv.URL,
		APIKey: "sk-test", Model: "bge-large-zh", Dims: 2,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingProvider: %v", err)
	}

	vecs, err := p.Embed(context.Background(), []string{"用户偏好简洁回复", "用户在上海工作"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/embeddings" {
		t.Errorf("path = %q, want /embeddings", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q, want Bearer sk-test", gotAuth)
	}
	if gotModel != "bge-large-zh" {
		t.Errorf("model = %q", gotModel)
	}
	if gotInputCount != 2 {
		t.Errorf("input count = %d, want 2 (批量)", gotInputCount)
	}
	if len(vecs) != 2 || vecs[0][0] != 0.1 || vecs[1][0] != 0.4 {
		t.Errorf("vectors not sorted by index: %v", vecs)
	}
	if p.Dim() != 2 {
		t.Errorf("Dim = %d, want 2", p.Dim())
	}
	if p.Name() == "" || !strings.Contains(p.Name(), "bge-large-zh") {
		t.Errorf("Name = %q, 应含模型标识(向量模型标记,§6.3)", p.Name())
	}
}

// TestOpenAICompatEmbedding_DimMismatch 向量维度与声明不符 → 明确报错(fail-fast,§5.4)。
func TestOpenAICompatEmbedding_DimMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[0.1,0.2,0.3]}]}`))
	}))
	defer srv.Close()

	p, _ := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: "openai_compatible", BaseURL: srv.URL, Model: "m", Dims: 2,
	})
	_, err := p.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "dim") {
		t.Errorf("期望维度不符报错, got %v", err)
	}
}

// TestOpenAICompatEmbedding_HTTPError 上游非 2xx → error(降级链由调用方处理)。
func TestOpenAICompatEmbedding_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad key"}`))
	}))
	defer srv.Close()

	p, _ := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: "openai_compatible", BaseURL: srv.URL, Model: "m", Dims: 2,
	})
	if _, err := p.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("上游 401 应返回 error")
	}
}

// TestOllamaEmbedding 本地 Ollama /api/embed 端点。
func TestOllamaEmbedding(t *testing.T) {
	var gotPath, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var req struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotModel = req.Model
		_, _ = w.Write([]byte(`{"embeddings":[[0.1,0.2],[0.3,0.4]]}`))
	}))
	defer srv.Close()

	p, err := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: "ollama", BaseURL: srv.URL, Model: "qwen3-embedding:0.6b", Dims: 2,
	})
	if err != nil {
		t.Fatalf("NewEmbeddingProvider: %v", err)
	}
	vecs, err := p.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if gotPath != "/api/embed" {
		t.Errorf("path = %q, want /api/embed", gotPath)
	}
	if gotModel != "qwen3-embedding:0.6b" {
		t.Errorf("model = %q", gotModel)
	}
	if len(vecs) != 2 || vecs[1][1] != 0.4 {
		t.Errorf("vectors = %v", vecs)
	}
}

// TestNewEmbeddingProvider_Dispatch 装配选择器:按 provider 分发;非法配置 fail-fast;未配置返回 nil(禁用)。
func TestNewEmbeddingProvider_Dispatch(t *testing.T) {
	cases := []struct {
		name    string
		cfg     EmbeddingProviderConfig
		wantNil bool
		wantErr bool
	}{
		{name: "openai_compatible", cfg: EmbeddingProviderConfig{Provider: "openai_compatible", BaseURL: "http://x", Model: "m", Dims: 2}},
		{name: "ollama", cfg: EmbeddingProviderConfig{Provider: "ollama", BaseURL: "http://x", Model: "m", Dims: 2}},
		{name: "未配置→nil(禁用,检索降级子串)", cfg: EmbeddingProviderConfig{}, wantNil: true},
		{name: "未知provider", cfg: EmbeddingProviderConfig{Provider: "foo", BaseURL: "http://x", Model: "m", Dims: 2}, wantErr: true},
		{name: "缺model", cfg: EmbeddingProviderConfig{Provider: "ollama", BaseURL: "http://x", Dims: 2}, wantErr: true},
		{name: "缺base_url", cfg: EmbeddingProviderConfig{Provider: "ollama", Model: "m", Dims: 2}, wantErr: true},
		{name: "dims非法", cfg: EmbeddingProviderConfig{Provider: "ollama", BaseURL: "http://x", Model: "m"}, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := NewEmbeddingProvider(c.cfg)
			if c.wantErr && err == nil {
				t.Error("期望报错, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("意外报错: %v", err)
			}
			if c.wantNil && p != nil {
				t.Errorf("期望 nil provider, got %T", p)
			}
		})
	}
}

// TestEmbeddingProvider_Timeout 超时控制生效:上游挂起,Embed 在超时后返回 error。
func TestEmbeddingProvider_Timeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()

	p, _ := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: "openai_compatible", BaseURL: srv.URL, Model: "m", Dims: 2, Timeout: 50 * time.Millisecond,
	})
	if _, err := p.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("上游挂起应触发超时 error")
	}
}
