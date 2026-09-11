package memory

import (
	"context"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 消息嵌入器(M7 中期记忆 §10.5):对话轮次结束异步增量嵌入新消息。
//
//	NotifyTurn(异步) → 独立水位检查 → (last, latest] 未嵌入消息
//	  → 按 8 条/批向量化([role] content,1500 字符截断) → upsert → 逐批推进水位
//
// 与沉淀管线互不依赖:独立水位、独立降级——embedding 未配置/失败时静默跳过,
// 不影响沉淀、不影响对话。存量回填无需专门任务:水位 0 首轮自然全量处理。

const (
	embedBatchSize  = 8    // 与实验(cmd/embedexp)一致的批大小
	embedMaxRuneLen = 1500 // 单条嵌入文本上限(超长截断,防 token 爆炸)
)

// MessageEmbedder 消息级向量写入器。实现 chat.TurnSink。
type MessageEmbedder struct {
	watermarkRepo memoryrepo.EmbeddingWatermarkRepository
	embRepo       memoryrepo.MessageEmbeddingRepository
	source        ConversationSource // 复用沉淀管线的消息区间接口
	embedding     EmbeddingProvider
	// embeddingResolver 用户级向量解析(与沉淀管线同源);非 nil 且返回非 nil 时优先
	embeddingResolver func(userID int64) EmbeddingProvider
	inflight          sync.Map // userID → struct{} (per-user 单飞)
}

func NewMessageEmbedder(
	watermarkRepo memoryrepo.EmbeddingWatermarkRepository,
	embRepo memoryrepo.MessageEmbeddingRepository,
	source ConversationSource,
	embedding EmbeddingProvider,
) *MessageEmbedder {
	return &MessageEmbedder{
		watermarkRepo: watermarkRepo,
		embRepo:       embRepo,
		source:        source,
		embedding:     embedding,
	}
}

// SetEmbeddingResolver 注入用户级向量解析器(装配点调用,复用用户向量配置缓存)。
func (e *MessageEmbedder) SetEmbeddingResolver(r func(userID int64) EmbeddingProvider) {
	e.embeddingResolver = r
}

func (e *MessageEmbedder) providerFor(userID int64) EmbeddingProvider {
	if e.embeddingResolver != nil {
		if ep := e.embeddingResolver(userID); ep != nil {
			return ep
		}
	}
	return e.embedding
}

// NotifyTurn 对话轮次结束钩子:异步触发,绝不阻塞对话,绝不 panic 外泄。
func (e *MessageEmbedder) NotifyTurn(userID int64) {
	if _, busy := e.inflight.LoadOrStore(userID, struct{}{}); busy {
		return // per-user 单飞:上轮没跑完不叠跑(下轮消息下一轮补)
	}
	go func() {
		defer e.inflight.Delete(userID)
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("memory: 消息嵌入 panic",
					zap.Int64("user_id", userID), zap.Any("recover", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := e.RunOnce(ctx, userID); err != nil {
			logger.WarnWithFields("memory: 消息嵌入本轮未完成,水位停在失败批次前,下轮重试",
				zap.Int64("user_id", userID), zap.Error(err))
		}
	}()
}

// RunOnce 执行一轮增量嵌入:从水位到最新,逐批嵌入落库推进。返回错误表示中途失败(已完成的批次已持久化)。
func (e *MessageEmbedder) RunOnce(ctx context.Context, userID int64) error {
	provider := e.providerFor(userID)
	if provider == nil {
		return nil // 中期层降级:未配置 embedding 则静默缺失(§10.5)
	}
	wm, err := e.watermarkRepo.GetByUserID(userID)
	if err != nil {
		return err
	}
	latest, err := e.source.GetLatestMessageID(userID)
	if err != nil {
		return err
	}
	if latest <= wm.LastEmbeddedMsgID {
		return nil // 无新消息
	}
	msgs, err := e.source.GetRangeByUserID(userID, wm.LastEmbeddedMsgID, latest)
	if err != nil {
		return err
	}
	// 只嵌对话消息;空文本跳过
	type pending struct {
		id   int64
		role string
		text string
	}
	var queue []pending
	for _, m := range msgs {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		content := m.Content
		if strings.TrimSpace(content) == "" {
			continue
		}
		if utf8.RuneCountInString(content) > embedMaxRuneLen {
			content = string([]rune(content)[:embedMaxRuneLen]) // 按 rune 截断,防切坏多字节
		}
		queue = append(queue, pending{id: m.ID, role: m.Role, text: "[" + m.Role + "] " + content})
	}
	for i := 0; i < len(queue); i += embedBatchSize {
		end := i + embedBatchSize
		if end > len(queue) {
			end = len(queue)
		}
		batch := queue[i:end]
		texts := make([]string, len(batch))
		for j, p := range batch {
			texts[j] = p.text
		}
		vecs, err := provider.Embed(ctx, texts)
		if err != nil {
			return err // 该批失败:错误上抛,水位停在上一批(重试只补失败区间)
		}
		embs := make([]*memorydomain.MessageEmbedding, len(batch))
		for j, p := range batch {
			embs[j] = &memorydomain.MessageEmbedding{
				MessageID: p.id, UserID: userID, Role: p.role,
				Embedding: vecs[j], EmbeddingModel: provider.Name(),
				CreatedAt: time.Now(),
			}
		}
		if err := e.embRepo.UpsertBatch(embs); err != nil {
			return err
		}
		if err := e.watermarkRepo.Save(userID, batch[len(batch)-1].id); err != nil {
			return err
		}
	}
	return nil
}
