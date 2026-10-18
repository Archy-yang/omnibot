package mcp

// mcp_service_test.go — 审计测试盲区补齐:providerFor 解析优先级 + embedToolDesc 容错。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	memoryservice "omnibot/internal/service/memory"
)

// stubEmbedding 可编程向量桩。
type stubEmbedding struct {
	name  string
	vecs  [][]float32
	err   error
	calls int
}

func (s *stubEmbedding) Embed(_ context.Context, _ []string) ([][]float32, error) {
	s.calls++
	return s.vecs, s.err
}
func (s *stubEmbedding) Dim() int     { return len(s.vecs[0]) }
func (s *stubEmbedding) Name() string { return s.name }

func TestProviderFor_ResolverPriority(t *testing.T) {
	sys := &stubEmbedding{name: "system"}
	user := &stubEmbedding{name: "user"}
	svc := NewMCPService()
	svc.SetEmbeddingProvider(sys)

	// 无 resolver:回落系统默认
	assert.Equal(t, "system", svc.providerFor(1).Name())

	// resolver 返回用户级:优先
	svc.SetEmbeddingResolver(func(int64) memoryservice.EmbeddingProvider { return user })
	assert.Equal(t, "user", svc.providerFor(1).Name())

	// resolver 返回 nil:回落系统默认(12 号 §5.3 同语义)
	svc.SetEmbeddingResolver(func(int64) memoryservice.EmbeddingProvider { return nil })
	assert.Equal(t, "system", svc.providerFor(1).Name())
}

func TestEmbedToolDesc(t *testing.T) {
	t.Run("nil provider 返回空", func(t *testing.T) {
		vec, model := (&MCPService{}).embedToolDesc(nil, "tool", "desc", "server")
		assert.Nil(t, vec)
		assert.Empty(t, model)
	})

	t.Run("空描述返回空", func(t *testing.T) {
		provider := &stubEmbedding{name: "m", vecs: [][]float32{{0.1, 0.2}}}
		vec, model := (&MCPService{}).embedToolDesc(provider, "tool", "  ", "server")
		assert.Nil(t, vec)
		assert.Empty(t, model)
		assert.Zero(t, provider.calls, "空描述不应发起向量化调用")
	})

	t.Run("成功返回向量与模型标识", func(t *testing.T) {
		provider := &stubEmbedding{name: "embed-model", vecs: [][]float32{{0.1, 0.2}}}
		vec, model := (&MCPService{}).embedToolDesc(provider, "tool", "desc", "server")
		require.NotNil(t, vec)
		assert.Equal(t, []float32{0.1, 0.2}, vec)
		assert.Equal(t, "embed-model", model)
	})

	t.Run("向量化失败返回空不 panic", func(t *testing.T) {
		provider := &stubEmbedding{name: "m", err: errors.New("endpoint down")}
		vec, model := (&MCPService{}).embedToolDesc(provider, "tool", "desc", "server")
		assert.Nil(t, vec)
		assert.Empty(t, model)
	})

	t.Run("向量数不匹配返回空", func(t *testing.T) {
		provider := &stubEmbedding{name: "m", vecs: nil}
		vec, model := (&MCPService{}).embedToolDesc(provider, "tool", "desc", "server")
		assert.Nil(t, vec)
		assert.Empty(t, model)
	})
}
