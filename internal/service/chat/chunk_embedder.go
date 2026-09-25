package chat

import (
	"context"
	"sync"
	"time"
	"unicode/utf8"

	chatrepo "omnibot/internal/repository/chat"
	memoryservice "omnibot/internal/service/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// chunkEmbedMaxRunes 单 chunk 嵌入文本的 rune 上限(防超长输入被 embedding 端点拒绝)。
const chunkEmbedMaxRunes = 2000

// ChunkEmbedder 对话召回块构建器(Phase 3,§9.2/§9.3)。实现 chat.TurnSink。
//
// 与 MessageEmbedder 同款增量模式:水位之后的新消息按 turn 分组,受影响的 turn
// 整体重建 chunk(迟到 report 归属旧 turn 时自动重嵌)。
// 嵌入失败水位不推进,下轮重试;构建异步,绝不阻塞对话。
type ChunkEmbedder struct {
	watermarkRepo chatrepo.ChunkWatermarkRepository
	chunkRepo     chatrepo.ConversationChunkRepository
	msgRepo       chatrepo.MessageRepository // GetLatestMessageID/GetRangeByUserID/GetByTurnID
	embedding     memoryservice.EmbeddingProvider
	// embeddingResolver 用户级向量解析(与沉淀/消息嵌入同源);非 nil 且返回非 nil 时优先
	embeddingResolver func(userID int64) memoryservice.EmbeddingProvider
	inflight          sync.Map // userID → struct{} (per-user 单飞)
}

func NewChunkEmbedder(
	watermarkRepo chatrepo.ChunkWatermarkRepository,
	chunkRepo chatrepo.ConversationChunkRepository,
	msgRepo chatrepo.MessageRepository,
	embedding memoryservice.EmbeddingProvider,
) *ChunkEmbedder {
	return &ChunkEmbedder{
		watermarkRepo: watermarkRepo,
		chunkRepo:     chunkRepo,
		msgRepo:       msgRepo,
		embedding:     embedding,
	}
}

// SetEmbeddingResolver 注入用户级向量解析(wire 装配)。
func (e *ChunkEmbedder) SetEmbeddingResolver(r func(userID int64) memoryservice.EmbeddingProvider) {
	e.embeddingResolver = r
}

func (e *ChunkEmbedder) providerFor(userID int64) memoryservice.EmbeddingProvider {
	if e.embeddingResolver != nil {
		if p := e.embeddingResolver(userID); p != nil {
			return p
		}
	}
	return e.embedding
}

// NotifyTurn 对话轮次结束钩子:异步触发,绝不阻塞对话,绝不 panic 外泄。
func (e *ChunkEmbedder) NotifyTurn(userID int64) {
	if _, busy := e.inflight.LoadOrStore(userID, struct{}{}); busy {
		return // per-user 单飞:上轮没跑完不叠跑(下轮消息下一轮补)
	}
	go func() {
		defer e.inflight.Delete(userID)
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("chunk: 召回块构建 panic",
					zap.Int64("user_id", userID), zap.Any("recover", r))
			}
		}()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := e.RunOnce(ctx, userID); err != nil {
			logger.WarnWithFields("chunk: 召回块构建本轮未完成,水位停在失败 turn 前,下轮重试",
				zap.Int64("user_id", userID), zap.Error(err))
		}
	}()
}

// RunOnce 执行一轮增量构建:水位之后的新消息按 turn 分组,逐 turn 重建 chunk。
// 返回错误表示中途失败(已完成的 turn 已持久化,水位停在失败 turn 之前)。
func (e *ChunkEmbedder) RunOnce(ctx context.Context, userID int64) error {
	provider := e.providerFor(userID)
	if provider == nil {
		return nil // 召回层降级:未配置 embedding 则静默缺失
	}
	wm, err := e.watermarkRepo.GetByUserID(userID)
	if err != nil {
		return err
	}
	latest, err := e.msgRepo.GetLatestMessageID(userID)
	if err != nil {
		return err
	}
	if latest <= wm.LastProcessedMsgID {
		return nil // 无新消息
	}
	msgs, err := e.msgRepo.GetRangeByUserID(userID, wm.LastProcessedMsgID, latest)
	if err != nil {
		return err
	}

	// 受影响的 turn(保序去重):有 turn 归属的才进 chunk
	var turnIDs []int64
	seen := map[int64]bool{}
	for _, m := range msgs {
		if m.TurnID == nil || seen[*m.TurnID] {
			continue
		}
		seen[*m.TurnID] = true
		turnIDs = append(turnIDs, *m.TurnID)
	}

	for _, turnID := range turnIDs {
		if err := e.rebuildTurn(ctx, userID, provider, turnID); err != nil {
			return err
		}
	}
	return e.watermarkRepo.Save(userID, latest)
}

// rebuildTurn 取全 turn 消息 → 切分 → 批量嵌入 → 原子替换。
// 取全量(而非仅水位后)的原因:迟到 report 归属旧 turn,该 turn 其余消息早于水位。
func (e *ChunkEmbedder) rebuildTurn(ctx context.Context, userID int64, provider memoryservice.EmbeddingProvider, turnID int64) error {
	msgs, err := e.msgRepo.GetByTurnID(userID, turnID)
	if err != nil {
		return err
	}
	convID := int64(0)
	for _, m := range msgs {
		if m.ConversationID != nil {
			convID = *m.ConversationID
			break
		}
	}
	if convID == 0 {
		return nil // 无 conversation 归属(存量异常数据):跳过
	}
	chunks := buildTurnChunks(userID, convID, turnID, msgs)
	if len(chunks) == 0 {
		return e.chunkRepo.ReplaceTurnChunks(turnID, nil)
	}

	texts := make([]string, 0, len(chunks))
	for _, c := range chunks {
		text := c.Content
		if utf8.RuneCountInString(text) > chunkEmbedMaxRunes {
			text = string([]rune(text)[:chunkEmbedMaxRunes]) // 按 rune 截断,防切坏多字节
		}
		texts = append(texts, text)
	}
	vecs, err := provider.Embed(ctx, texts)
	if err != nil {
		return err
	}
	for i, c := range chunks {
		c.Embedding = vecs[i]
		c.EmbeddingModel = provider.Name()
	}
	return e.chunkRepo.ReplaceTurnChunks(turnID, chunks)
}
