package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// EmbeddingProvider 文本向量化抽象(12-记忆系统技术方案 §5.1)。
//
// 不绑定本地/云端:OpenAI 兼容端点(百度千帆 v2 / DashScope / SiliconFlow / OpenAI)、
// 本地 Ollama 均为实现,装配走 NewEmbeddingProvider。可用性降级链见 §5.4:
// Embed 失败由调用方决定仅存文本,provider 本身不做静默降级。
type EmbeddingProvider interface {
	// Embed 批量文本向量化,返回向量顺序与输入一致。
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	// Dim 向量维度(构造时声明,Embed 返回值逐条校验,fail-fast)
	Dim() int
	// Name 模型标识(落 EmbeddingModel 列;检索只比同标识向量,§6.3)
	Name() string
}

// EmbeddingProviderConfig provider 装配参数(routes.go 从 pkg/config 适配)。
type EmbeddingProviderConfig struct {
	Provider string // ""=禁用(检索降级子串) | openai_compatible | ollama
	BaseURL  string
	APIKey   string
	Model    string
	Dims     int
	Timeout  time.Duration // <=0 回落 10s
}

const defaultEmbeddingTimeout = 10 * time.Second

// NewEmbeddingProvider 按 cfg.Provider 构造;未配置返回 (nil, nil) 表示功能关闭。
func NewEmbeddingProvider(cfg EmbeddingProviderConfig) (EmbeddingProvider, error) {
	if cfg.Provider == "" {
		return nil, nil
	}
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("embedding base_url 不能为空 (provider=%s)", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("embedding model 不能为空 (provider=%s)", cfg.Provider)
	}
	if cfg.Dims <= 0 {
		return nil, fmt.Errorf("embedding dims 必须为正整数, got %d", cfg.Dims)
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultEmbeddingTimeout
	}
	base := embeddingBase{
		baseURL: strings.TrimSuffix(cfg.BaseURL, "/"),
		apiKey:  cfg.APIKey,
		model:   cfg.Model,
		dims:    cfg.Dims,
		client:  &http.Client{Timeout: timeout},
	}
	switch cfg.Provider {
	case "openai_compatible":
		return &openAICompatEmbedding{embeddingBase: base, name: cfg.Provider + "/" + cfg.Model}, nil
	case "ollama":
		return &ollamaEmbedding{embeddingBase: base, name: cfg.Provider + "/" + cfg.Model}, nil
	default:
		return nil, fmt.Errorf("未知 embedding provider: %q (支持: openai_compatible, ollama)", cfg.Provider)
	}
}

// embeddingBase 两个 HTTP 实现的公共部分。
type embeddingBase struct {
	baseURL string
	apiKey  string
	model   string
	dims    int
	client  *http.Client
}

func (b embeddingBase) Dim() int { return b.dims }
func (b embeddingBase) post(ctx context.Context, path string, payload interface{}) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if b.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+b.apiKey)
	}
	resp, err := b.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embedding 上游 %d: %s", resp.StatusCode, truncateForLog(string(data)))
	}
	return data, nil
}

// validateVectors 维度逐条校验(声明与实际不符立即报错,防止异构向量污染库)。
func (b embeddingBase) validateVectors(vecs [][]float32) error {
	for i, v := range vecs {
		if len(v) != b.dims {
			return fmt.Errorf("embedding dim 不符: 上游返回 %d 维, 声明 %d 维 (index=%d)", len(v), b.dims, i)
		}
	}
	return nil
}

func truncateForLog(s string) string {
	if len(s) > 200 {
		return s[:200]
	}
	return s
}

// openAICompatEmbedding OpenAI 兼容 /v1/embeddings 端点(千帆 v2/DashScope/SiliconFlow/OpenAI)。
type openAICompatEmbedding struct {
	embeddingBase
	name string
}

func (p *openAICompatEmbedding) Name() string { return p.name }

func (p *openAICompatEmbedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	data, err := p.post(ctx, "/embeddings", map[string]interface{}{
		"model": p.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 embedding 响应: %w", err)
	}
	// 按 index 重排(上游不保证有序)
	sort.Slice(parsed.Data, func(i, j int) bool { return parsed.Data[i].Index < parsed.Data[j].Index })
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embedding 数量不符: 输入 %d, 返回 %d", len(texts), len(parsed.Data))
	}
	vecs := make([][]float32, len(parsed.Data))
	for i, d := range parsed.Data {
		vecs[i] = d.Embedding
	}
	if err := p.validateVectors(vecs); err != nil {
		return nil, err
	}
	return vecs, nil
}

// ollamaEmbedding 本地 Ollama /api/embed 端点。
type ollamaEmbedding struct {
	embeddingBase
	name string
}

func (p *ollamaEmbedding) Name() string { return p.name }

func (p *ollamaEmbedding) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	data, err := p.post(ctx, "/api/embed", map[string]interface{}{
		"model": p.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("解析 ollama embedding 响应: %w", err)
	}
	if len(parsed.Embeddings) != len(texts) {
		return nil, fmt.Errorf("embedding 数量不符: 输入 %d, 返回 %d", len(texts), len(parsed.Embeddings))
	}
	if err := p.validateVectors(parsed.Embeddings); err != nil {
		return nil, err
	}
	return parsed.Embeddings, nil
}
