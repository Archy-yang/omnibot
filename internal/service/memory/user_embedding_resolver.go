package memory

// user_embedding_resolver.go — 用户级向量配置解析器(12 号 §5.3;2026-10 审计 #4 迁出 API 层)。

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"sync"

	"omnibot/pkg/logger"

	"go.uber.org/zap"

	userService "omnibot/internal/service/user"
)

// UserEmbeddingResolver 用户级向量配置解析器:
// 用户级配置完整 → 构造 provider(带指纹缓存);未配置/异常 → nil(调用方回落系统默认)。
type UserEmbeddingResolver struct {
	Svc userService.LLMConfigService
	mu  sync.Mutex
	// cache: userID → {配置指纹, provider}。指纹变了自动失效(用户改配置后无需重启)。
	cache map[int64]struct {
		fingerprint string
		provider    EmbeddingProvider
	}
}

// NewUserEmbeddingResolver 创建解析器。
func NewUserEmbeddingResolver(svc userService.LLMConfigService) *UserEmbeddingResolver {
	return &UserEmbeddingResolver{Svc: svc, cache: make(map[int64]struct {
		fingerprint string
		provider    EmbeddingProvider
	})}
}

// ResolveEmbeddingProvider 实现装配点要求的解析签名。
func (r *UserEmbeddingResolver) ResolveEmbeddingProvider(userID int64) EmbeddingProvider {
	cfg, ok, err := r.Svc.GetEmbeddingConfigForUser(userID)
	if err != nil || !ok {
		return nil
	}
	// 指纹 = 关键字段拼接 + key 哈希(不含明文)
	sum := sha1.Sum([]byte(cfg.Provider + "|" + cfg.BaseURL + "|" + cfg.Model + "|" + strconv.Itoa(cfg.Dims) + "|" + cfg.APIKey))
	fingerprint := hex.EncodeToString(sum[:])

	r.mu.Lock()
	cached, hit := r.cache[userID]
	r.mu.Unlock()
	if hit && cached.fingerprint == fingerprint && cached.provider != nil {
		return cached.provider
	}

	provider, err := NewEmbeddingProvider(EmbeddingProviderConfig{
		Provider: cfg.Provider,
		BaseURL:  cfg.BaseURL,
		APIKey:   cfg.APIKey,
		Model:    cfg.Model,
		Dims:     cfg.Dims,
	})
	if err != nil {
		logger.Warn("用户级向量配置构造失败,回落系统默认",
			zap.Int64("user_id", userID), zap.Error(err))
		provider = nil
	}
	r.mu.Lock()
	r.cache[userID] = struct {
		fingerprint string
		provider    EmbeddingProvider
	}{fingerprint, provider}
	r.mu.Unlock()
	return provider
}
