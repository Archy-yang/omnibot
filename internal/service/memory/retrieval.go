package memory

import (
	"context"
	"math"
	"sort"
	"strings"
	"time"

	"omnibot/internal/domain/conversation"
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
	// 事项检索(M6.2 两段式):标题子串命中是强信号(用户提起事项名),独立加成;
	// 达到 matterHitThreshold 才算命中——避免每个查询都勉强凑出一个事项。
	matterTitleBonus   = 0.3
	matterDescBonus    = 0.1
	matterHitThreshold = 0.25
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
		if m.IsClosedLoop() {
			continue // closed 的 loop 不进检索(§14.2.2),管理面可见可重开
		}
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

// SearchMatters 事项优先检索(M6.2 两段式第一段):
// 向量(事项 Title+StateDesc)+ 标题/状态子串融合打分,达阈值的按分降序取 topK;
// 每个命中事项挂出其关联原子记忆全景("这件事到哪了"直接得到完整答案)。
func (s *memoryService) SearchMatters(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MatterHit, error) {
	if s.matterRepo == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	matters, err := s.matterRepo.ListActiveByUserID(userID)
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
	hits := make([]memorydomain.MatterHit, 0, len(matters))
	for _, m := range matters {
		score := 0.0
		if qvec != nil && len(m.Embedding) > 0 && m.EmbeddingModel == currentModel {
			score = CosineSimilarity(qvec, m.Embedding)
		}
		if strings.Contains(strings.ToLower(m.Title), lowered) {
			score += matterTitleBonus
		}
		if strings.Contains(strings.ToLower(m.StateDesc), lowered) {
			score += matterDescBonus
		}
		if score < matterHitThreshold {
			continue
		}
		facts, err := s.repo.ListByUserIDAndMatter(userID, m.ID)
		if err != nil {
			return nil, err
		}
		hits = append(hits, memorydomain.MatterHit{Matter: m, Facts: facts, Score: score})
	}

	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// midtermRecencyHalfLife 中期时间加权半衰期(M7 §10.6):30 天前的消息权重减半。
const midtermRecencyHalfLife = 30 * 24 * time.Hour

// SearchRecentMessages 中期记忆检索(M7 §10.6):消息级向量余弦 + 时间加权,
// score = 0.8×cos + 0.2×recency(recency 指数半衰,30 天减半)。
// 原文不落向量表——按余弦取候选,回表取 content/时间(零抽象,细节永不丢)。
// embedding 未配置/无向量/回表失败 → 返回空(中期层静默缺失,不报错不阻塞长期/事项)。
func (s *memoryService) SearchRecentMessages(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MessageHit, error) {
	if s.msgEmbRepo == nil || s.msgSource == nil {
		return nil, nil
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	provider := s.resolveProvider(userID)
	if provider == nil {
		return nil, nil
	}
	qvec := s.embedQuery(ctx, provider, query)
	if qvec == nil {
		return nil, nil
	}
	embs, err := s.msgEmbRepo.ListByUserID(userID)
	if err != nil {
		return nil, err
	}

	// 第一遍:纯余弦取候选(4×topK),避免为 recency 回表全量消息
	currentModel := provider.Name()
	type cand struct {
		msgID int64
		cos   float64
	}
	cands := make([]cand, 0, len(embs))
	for _, e := range embs {
		if e.EmbeddingModel != currentModel || len(e.Embedding) == 0 {
			continue
		}
		cands = append(cands, cand{msgID: e.MessageID, cos: CosineSimilarity(qvec, e.Embedding)})
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].cos > cands[j].cos })
	candN := topK * 4
	if candN == 0 || candN > len(cands) {
		candN = len(cands)
	}
	cands = cands[:candN]

	// 回表取原文与时间,做时间加权后重排
	ids := make([]int64, len(cands))
	for i, c := range cands {
		ids[i] = c.msgID
	}
	msgs, err := s.msgSource.GetByIDs(ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*conversation.Message, len(msgs))
	for _, m := range msgs {
		byID[m.ID] = m
	}
	now := time.Now()
	hits := make([]memorydomain.MessageHit, 0, len(cands))
	for _, c := range cands {
		m, ok := byID[c.msgID]
		if !ok {
			continue // 消息已删除
		}
		recency := 0.5
		if elapsed := now.Sub(m.CreatedAt); elapsed > 0 {
			recency = math.Pow(0.5, float64(elapsed)/float64(midtermRecencyHalfLife))
		}
		score := 0.8*c.cos + 0.2*recency
		hits = append(hits, memorydomain.MessageHit{
			MessageID: m.ID, Role: m.Role, Content: m.Content,
			CreatedAt: m.CreatedAt, Score: score,
		})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if topK > 0 && len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}
