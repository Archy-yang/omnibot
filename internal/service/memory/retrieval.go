package memory

import (
	"context"
	"math"
	"sort"
	"strings"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 检索管线(12-记忆系统技术方案 §6.4/§8):
//
//	score = 余弦相似度(语义,仅同 EmbeddingModel 向量可比) + 子串命中加成(辅路)
//	语义分 > 子串分;embedding 不可用/失败 → 纯子串降级,记忆照常可检索。

const (
	// substringBonus 子串命中的加成分(语义满分为 1,加成必须明显小于语义差值)
	substringBonus = 0.1
)

// CosineSimilarity 余弦相似度;零向量/长度不符返回 0(不可比,不报错)。
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		normA += float64(a[i]) * float64(a[i])
		normB += float64(b[i]) * float64(b[i])
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// resolveProvider 检索时生效的 provider:用户级命中优先,否则系统默认(§5.3)。
func (s *memoryService) resolveProvider(userID int64) EmbeddingProvider {
	if s.resolver != nil {
		if p := s.resolver.ResolveEmbeddingProvider(userID); p != nil {
			return p
		}
	}
	return s.embedding
}

// embedQuery 查询向量化。provider 未配置或失败返回 nil(降级子串),不阻塞检索。
func (s *memoryService) embedQuery(ctx context.Context, provider EmbeddingProvider, query string) []float32 {
	if provider == nil {
		return nil
	}
	vecs, err := provider.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		logger.WarnWithFields("memory: 查询向量化失败,降级子串检索",
			zap.String("operation", "memory_search"),
			zap.Error(err),
		)
		return nil
	}
	return vecs[0]
}

// SearchMemories 语义+子串融合检索长期记忆,按分数降序取 topK。
// 只与 EmbeddingModel == 当前生效 provider 的向量比较(§6.3,异构模型向量不可比)。
func (s *memoryService) SearchMemories(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MemoryHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	memories, err := s.repo.ListByUserID(userID)
	if err != nil {
		return nil, err
	}

	provider := s.resolveProvider(userID)
	qvec := s.embedQuery(ctx, provider, query)
	var currentModel string
	if provider != nil {
		currentModel = provider.Name()
	}

	lowered := strings.ToLower(query)
	hits := make([]memorydomain.MemoryHit, 0, len(memories))
	for _, m := range memories {
		score := 0.0
		if qvec != nil && len(m.Embedding) > 0 && m.EmbeddingModel == currentModel {
			score = CosineSimilarity(qvec, m.Embedding)
		}
		if strings.Contains(strings.ToLower(m.Content), lowered) {
			score += substringBonus
		}
		if score > 0 {
			hits = append(hits, memorydomain.MemoryHit{Memory: m, Score: score})
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// SearchDigests 语义+子串融合检索对话纪要(中期),按分数降序取 topK。
func (s *memoryService) SearchDigests(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.DigestHit, error) {
	query = strings.TrimSpace(query)
	if query == "" || s.digestRepo == nil {
		return nil, nil
	}
	digests, err := s.digestRepo.ListActiveByUserID(userID)
	if err != nil {
		return nil, err
	}

	provider := s.resolveProvider(userID)
	qvec := s.embedQuery(ctx, provider, query)
	var currentModel string
	if provider != nil {
		currentModel = provider.Name()
	}

	lowered := strings.ToLower(query)
	hits := make([]memorydomain.DigestHit, 0, len(digests))
	for _, d := range digests {
		score := 0.0
		if qvec != nil && len(d.Embedding) > 0 && d.EmbeddingModel == currentModel {
			score = CosineSimilarity(qvec, d.Embedding)
		}
		if strings.Contains(strings.ToLower(d.Summary), lowered) {
			score += substringBonus
		}
		if score > 0 {
			hits = append(hits, memorydomain.DigestHit{Digest: d, Score: score})
		}
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}
