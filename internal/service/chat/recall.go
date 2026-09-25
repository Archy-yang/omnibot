package chat

import (
	"context"
	"sort"
	"strings"

	"omnibot/internal/domain/conversation"
	chatrepo "omnibot/internal/repository/chat"
	memoryservice "omnibot/internal/service/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// Conversation Recall(Phase 3,16-架构迭代路线图 §8/§9):
// Runtime 自动执行的 shallow retrieval——按当前问题从 Raw Conversation 的 chunk 索引
// 中召回可能被 Compact 截掉的历史原文,补偿 Compact 的有损性。
// 与 memory_search 分层:Recall 是自动单次检索原始对话;memory_search 是 Agent 主动
// 多轮检索长期语义记忆。

const (
	// recallTopK 命中 chunk 数上限(展开邻居前)。
	recallTopK = 3
	// recallScoreThreshold 语义命中阈值:低于此分的历史与当前问题无关,宁缺勿滥。
	recallScoreThreshold = 0.35
	// recallMaxChunks 注入 chunk 总量上限(含邻居展开)。
	recallMaxChunks = 6
)

// ConversationChunkHit 命中的召回块。
type ConversationChunkHit struct {
	Chunk *conversation.ConversationChunk
	Score float64
}

// RecallSearcher 召回检索抽象(BuildContextMessages 注入用;测试可桩)。
// excludeBeforeMessageID:尾窗已完整覆盖的消息 id 上界,覆盖范围内的 chunk 不重复召回
// (Recall 的职责是补偿 Compact 截掉的部分,不是复述尾窗)。
type RecallSearcher interface {
	Search(ctx context.Context, userID int64, query string, excludeBeforeMessageID int64) ([]string, error)
}

// ChunkRecallService chunk 向量检索 + 邻居展开(§9.4/§9.6,V1 纯向量)。
// "Vector 用于定位,Conversation Store 用于恢复完整局部语境"——邻居 turn 一并展开。
type ChunkRecallService struct {
	chunkRepo chatrepo.ConversationChunkRepository
	embedding memoryservice.EmbeddingProvider
	// embeddingResolver 用户级向量解析(与沉淀/嵌入同源);非 nil 且返回非 nil 时优先
	embeddingResolver func(userID int64) memoryservice.EmbeddingProvider
	topK              int
	threshold         float64
}

func NewChunkRecallService(chunkRepo chatrepo.ConversationChunkRepository, embedding memoryservice.EmbeddingProvider) *ChunkRecallService {
	return &ChunkRecallService{
		chunkRepo: chunkRepo,
		embedding: embedding,
		topK:      recallTopK,
		threshold: recallScoreThreshold,
	}
}

// SetEmbeddingResolver 注入用户级向量解析(wire 装配)。
func (s *ChunkRecallService) SetEmbeddingResolver(r func(userID int64) memoryservice.EmbeddingProvider) {
	s.embeddingResolver = r
}

func (s *ChunkRecallService) providerFor(userID int64) memoryservice.EmbeddingProvider {
	if s.embeddingResolver != nil {
		if p := s.embeddingResolver(userID); p != nil {
			return p
		}
	}
	return s.embedding
}

// Search 召回与 query 语义相关的历史 chunk:
//  1. 全载余弦(仅同 EmbeddingModel 向量可比,§6.3)取 Top-K 达标命中;
//  2. 邻居 turn 展开(命中 turn 的前后各一个 turn);
//  3. 按对话时序输出,跳过尾窗已覆盖的 chunk,总量 recallMaxChunks 封顶。
// 无 provider/无命中/查询为空 → 返回 nil(静默降级,不阻塞上下文构建)。
func (s *ChunkRecallService) Search(ctx context.Context, userID int64, query string, excludeBeforeMessageID int64) ([]string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	provider := s.providerFor(userID)
	if provider == nil {
		return nil, nil
	}
	vecs, err := provider.Embed(ctx, []string{query})
	if err != nil || len(vecs) != 1 {
		logger.WarnWithFields("recall: 查询向量化失败,本轮跳过召回",
			zap.Int64("user_id", userID), zap.Error(err))
		return nil, nil
	}
	qvec := vecs[0]
	currentModel := provider.Name()

	chunks, err := s.chunkRepo.ListByUserID(userID)
	if err != nil {
		return nil, err
	}
	if len(chunks) == 0 {
		return nil, nil
	}

	// 评分 + Top-K(§9.6 V1)
	hits := make([]ConversationChunkHit, 0, len(chunks))
	for _, c := range chunks {
		if c.EmbeddingModel != currentModel || len(c.Embedding) == 0 {
			continue
		}
		score := memoryservice.CosineSimilarity(qvec, c.Embedding)
		if score >= s.threshold {
			hits = append(hits, ConversationChunkHit{Chunk: c, Score: score})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > s.topK {
		hits = hits[:s.topK]
	}
	if len(hits) == 0 {
		return nil, nil
	}

	// 邻居展开:chunks 已按 (turn_id, seq) 有序,命中 turn 的前后各一 turn
	turnOrder := make([]int64, 0, len(chunks))
	seenTurn := map[int64]bool{}
	for _, c := range chunks {
		if !seenTurn[c.TurnID] {
			seenTurn[c.TurnID] = true
			turnOrder = append(turnOrder, c.TurnID)
		}
	}
	index := map[int64]int{}
	for i, t := range turnOrder {
		index[t] = i
	}
	selectedTurns := map[int64]bool{}
	for _, h := range hits {
		t := h.Chunk.TurnID
		selectedTurns[t] = true
		if i := index[t]; i > 0 {
			selectedTurns[turnOrder[i-1]] = true
		}
		if i := index[t]; i < len(turnOrder)-1 {
			selectedTurns[turnOrder[i+1]] = true
		}
	}

	// 按对话时序输出;尾窗已覆盖的跳过;总量封顶
	out := make([]string, 0, recallMaxChunks)
	for _, c := range chunks {
		if len(out) >= recallMaxChunks {
			break
		}
		if !selectedTurns[c.TurnID] {
			continue
		}
		if c.EndMessageID <= excludeBeforeMessageID {
			continue // 已完整出现在 Recent Raw 尾窗,不复述
		}
		out = append(out, c.Content)
	}
	return out, nil
}
