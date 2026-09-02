package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 沉淀管线(12-记忆系统技术方案 §7):
//
//	对话轮次结束 → NotifyTurn(异步,不阻塞对话) → 水位检查
//	  → pending ≥ 阈值 → per-user 串行执行一轮:
//	      区间对话原文 → LLM 纪要 → digests 落库
//	                                  → LLM 记忆提取 → memories 落库(M2-C)
//	      → 推进水位(失败不推进,下轮重试同一区间)
//
// 纪要/提取用系统默认 LLM(系统能力,不随用户配置,§7.2);embedding 用系统默认 provider,
// 失败时纪要/记忆照常落库(仅无向量,读路径自动降级子串)。

// ConversationSource 对话消息区间读取(由 chat 消息仓储适配)。
type ConversationSource interface {
	GetLatestMessageID(userID int64) (int64, error)
	GetRangeByUserID(userID int64, afterID, toID int64) ([]*conversation.Message, error)
}

// PipelineLLM 管线用 LLM(非流式补全;由装配点适配系统默认对话模型)。
type PipelineLLM interface {
	Complete(ctx context.Context, system, user string) (string, error)
}

// DigestPipeline 沉淀管线。
type DigestPipeline struct {
	watermarkRepo memoryrepo.WatermarkRepository
	digestRepo    memoryrepo.DigestRepository
	memoryRepo    memoryrepo.MemoryRepository
	source        ConversationSource
	llm           PipelineLLM       // nil = 管线禁用(LLM 未装配)
	embedding     EmbeddingProvider // 系统默认,可 nil(降级无向量)
	threshold     int               // pending 消息数阈值
	inflight      sync.Map          // userID → struct{} (per-user 单飞标记)
}

func NewDigestPipeline(
	watermarkRepo memoryrepo.WatermarkRepository,
	digestRepo memoryrepo.DigestRepository,
	memoryRepo memoryrepo.MemoryRepository,
	source ConversationSource,
	llm PipelineLLM,
	embedding EmbeddingProvider,
	threshold int,
) *DigestPipeline {
	if threshold <= 0 {
		threshold = 20 // 攒批越大摊销越低,且更贴近"按对话段落"语义(§7 修订)
	}
	return &DigestPipeline{
		watermarkRepo: watermarkRepo,
		digestRepo:    digestRepo,
		memoryRepo:    memoryRepo,
		source:        source,
		llm:           llm,
		embedding:     embedding,
		threshold:     threshold,
	}
}

// NotifyTurn 对话轮次结束的钩子入口:异步触发,绝不阻塞对话,绝不 panic 外泄。
func (p *DigestPipeline) NotifyTurn(userID int64) {
	if p.llm == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("memory: 沉淀管线 panic",
					zap.Int64("user_id", userID), zap.Any("recover", r))
			}
		}()
		ctx := context.Background()
		if err := p.RunOnce(ctx, userID); err != nil {
			logger.WarnWithFields("memory: 沉淀管线本轮未完成,水位未推进,下轮重试",
				zap.Int64("user_id", userID), zap.Error(err))
		}
	}()
}

// RunOnce 同步执行一轮沉淀(导出供测试与手动触发)。
// 返回 error 仅表示"本轮未完成"(水位未推进,下轮重试),调用方可安全忽略。
func (p *DigestPipeline) RunOnce(ctx context.Context, userID int64) error {
	if p.llm == nil {
		return nil
	}
	// per-user 单飞:同一用户串行,不同用户互不影响
	if _, loaded := p.inflight.LoadOrStore(userID, struct{}{}); loaded {
		return nil
	}
	defer p.inflight.Delete(userID)

	latest, err := p.source.GetLatestMessageID(userID)
	if err != nil {
		return fmt.Errorf("读最新消息 ID: %w", err)
	}
	wm, err := p.watermarkRepo.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("读水位: %w", err)
	}
	pending := latest - wm.LastDigestMsgID
	if pending <= 0 || pending < int64(p.threshold) {
		return nil
	}

	fromID := wm.LastDigestMsgID // 区间语义 (from, to]
	toID := latest
	messages, err := p.source.GetRangeByUserID(userID, fromID, toID)
	if err != nil {
		return fmt.Errorf("读区间消息: %w", err)
	}
	if len(messages) == 0 {
		// 消息可能被清理:直接推进水位避免死循环
		return p.watermarkRepo.Upsert(userID, toID)
	}
	transcript := buildTranscript(messages)

	// 单次 LLM 调用同时产出纪要与记忆候选(§7 修订:合并调用)。
	// 调用失败或结果 schema 非法 → 整批作废,水位不动,下轮重试同一区间。
	resp, err := p.llm.Complete(ctx, pipelineSystemPrompt, transcript)
	if err != nil {
		return fmt.Errorf("沉淀调用失败: %w", err)
	}
	var parsed struct {
		Summary  string            `json:"summary"`
		Memories []memoryCandidate `json:"memories"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		return fmt.Errorf("沉淀结果 schema 非法,整批作废: %w", err)
	}

	// 中期:纪要落库
	summary := strings.TrimSpace(parsed.Summary)
	if summary != "" {
		digest := memorydomain.NewConversationDigest(userID, summary, fromID+1, toID, len(messages))
		p.stampEmbedding(digest, summary)
		if err := p.digestRepo.Create(digest); err != nil {
			return fmt.Errorf("落纪要: %w", err)
		}
	}

	// 长期:记忆提取落库(逐条容错,单条失败不影响其余)
	p.applyMemories(ctx, userID, parsed.Memories, fromID, toID)

	// 推进水位
	return p.watermarkRepo.Upsert(userID, toID)
}

// stampEmbedding 为纪要嵌入向量;embedding 未配置或失败 → 无向量落库(读路径降级子串)。
func (p *DigestPipeline) stampEmbedding(digest *memorydomain.ConversationDigest, text string) {
	if p.embedding == nil {
		return
	}
	vecs, err := p.embedding.Embed(context.Background(), []string{text})
	if err != nil || len(vecs) != 1 {
		logger.WarnWithFields("memory: 纪要向量化失败,落库为无向量",
			zap.Int64("user_id", digest.UserID), zap.Error(err))
		return
	}
	digest.Embedding = vecs[0]
	digest.EmbeddingModel = p.embedding.Name()
}

// buildTranscript 把区间消息拼成 LLM 可读的对话原文。
func buildTranscript(messages []*conversation.Message) string {
	var b strings.Builder
	b.WriteString("以下是用户与助手的一段对话原文(按时间正序):\n\n")
	for _, m := range messages {
		role := "用户"
		if m.Role == conversation.RoleAssistant {
			role = "助手"
		}
		fmt.Fprintf(&b, "[消息#%d|%s] %s\n", m.ID, role, m.Content)
	}
	return b.String()
}
