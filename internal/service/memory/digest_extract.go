package memory

import (
	"context"
	"strings"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 提取去重阈值(12-记忆系统技术方案 §7.3):
const (
	duplicateSkipThreshold  = 0.92 // 余弦 ≥ 0.92 视为重复 → 跳过
	conflictUpdateThreshold = 0.80 // 余弦 ∈ [0.80, 0.92) 视为疑似冲突 → 按新事实更新
)

// memoryCandidate LLM 提取的单条记忆候选(schema 与 pipelineSystemPrompt 对齐)。
type memoryCandidate struct {
	Content         string `json:"content"`
	SourceMessageID int64  `json:"source_message_id"`
}

// applyMemories 长期记忆提取落库(接收管线单次 LLM 调用解析出的候选)。
//
// 逐条过滤(空/超长/溯源越界) → 嵌入 → 与既有同模型记忆余弦比对
// (跳过/原位更新/新增)。单条失败仅记日志,不影响其余。宁漏勿错(PRD 红线)。
func (p *DigestPipeline) applyMemories(
	ctx context.Context,
	userID int64,
	candidates []memoryCandidate,
	fromID, toID int64,
) {
	if len(candidates) == 0 {
		return
	}

	existing, err := p.memoryRepo.ListByUserID(userID)
	if err != nil {
		logger.WarnWithFields("memory: 读既有记忆失败,放弃本批提取",
			zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	var currentModel string
	if p.embedding != nil {
		currentModel = p.embedding.Name()
	}

	for _, c := range candidates {
		content := strings.TrimSpace(c.Content)
		if content == "" || utf8.RuneCountInString(content) > MaxMemoryContentLength {
			continue
		}
		// 溯源校验:必须落在本次处理区间内,否则置 NULL(不可信的指针不如没有指针)
		var sourceMsgID *int64
		if c.SourceMessageID > fromID && c.SourceMessageID <= toID {
			id := c.SourceMessageID
			sourceMsgID = &id
		}

		// 嵌入候选(失败 → 无向量,仍可落库,读路径降级子串)
		var vec []float32
		if p.embedding != nil {
			if vecs, err := p.embedding.Embed(ctx, []string{content}); err == nil && len(vecs) == 1 {
				vec = vecs[0]
			} else {
				logger.WarnWithFields("memory: 候选记忆向量化失败,落库为无向量",
					zap.Int64("user_id", userID), zap.Error(err))
			}
		}

		dupID, action := classifyCandidate(existing, vec, currentModel, content)
		switch action {
		case candidateSkip:
			continue
		case candidateUpdate:
			if err := p.memoryRepo.UpdateContentEmbeddingByID(dupID, userID, content, vec, currentModel); err != nil {
				logger.WarnWithFields("memory: 疑似冲突更新失败,按新增处理",
					zap.Int64("user_id", userID), zap.Int64("memory_id", dupID), zap.Error(err))
				p.createAutoMemory(userID, content, sourceMsgID, vec, currentModel, &existing)
			} else {
				// 同步内存副本,影响后续候选的比对
				updateExistingInPlace(existing, dupID, content, vec, currentModel)
			}
		default:
			p.createAutoMemory(userID, content, sourceMsgID, vec, currentModel, &existing)
		}
	}
}

func (p *DigestPipeline) createAutoMemory(
	userID int64, content string, sourceMsgID *int64, vec []float32, model string, existing *[]*memorydomain.Memory,
) {
	m := memorydomain.NewAutoMemory(userID, content, sourceMsgID)
	if vec != nil {
		m.Embedding = vec
		m.EmbeddingModel = model
	}
	if err := p.memoryRepo.Create(m); err != nil {
		logger.WarnWithFields("memory: 自动记忆落库失败",
			zap.Int64("user_id", userID), zap.Error(err))
		return
	}
	*existing = append(*existing, m)
}

// candidateAction 单条候选的裁决结果。
type candidateAction int

const (
	candidateCreate candidateAction = iota
	candidateSkip
	candidateUpdate
)

// classifyCandidate 与既有记忆比对:重复跳过 / 疑似冲突更新 / 新增。
// 无向量时退化为精确同文判重(忽略大小写)。
func classifyCandidate(
	existing []*memorydomain.Memory,
	vec []float32,
	currentModel string,
	content string,
) (int64, candidateAction) {
	if vec == nil {
		for _, e := range existing {
			if strings.EqualFold(e.Content, content) {
				return e.ID, candidateSkip
			}
		}
		return 0, candidateCreate
	}

	updateID, updateScore := int64(0), 0.0
	for _, e := range existing {
		if e.EmbeddingModel != currentModel || len(e.Embedding) == 0 {
			continue // 异构模型向量不可比(§6.3)
		}
		score := CosineSimilarity(vec, e.Embedding)
		if score >= duplicateSkipThreshold {
			return e.ID, candidateSkip
		}
		if score >= conflictUpdateThreshold && score > updateScore {
			updateID, updateScore = e.ID, score
		}
	}
	if updateID > 0 {
		return updateID, candidateUpdate
	}
	return 0, candidateCreate
}

// updateExistingInPlace 原位更新既有记忆的内存副本(内容+向量),供后续候选比对。
func updateExistingInPlace(existing []*memorydomain.Memory, id int64, content string, vec []float32, model string) {
	for _, e := range existing {
		if e.ID == id {
			e.Content = content
			e.Embedding = vec
			e.EmbeddingModel = model
			return
		}
	}
}
