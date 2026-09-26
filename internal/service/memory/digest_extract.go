package memory

import (
	"strings"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// logMemoryWarn 记忆写入旁路告警的统一口径(不阻断主流程)。
func logMemoryWarn(userID int64, msg string, memoryID int64, err error) {
	logger.WarnWithFields("memory: "+msg,
		zap.Int64("user_id", userID), zap.Int64("memory_id", memoryID), zap.Error(err))
}

// 提取去重阈值(12-记忆系统技术方案 §7.3):
const (
	duplicateSkipThreshold  = 0.92 // 余弦 ≥ 0.92 视为重复 → 跳过
	conflictUpdateThreshold = 0.80 // 余弦 ∈ [0.80, 0.92) 视为疑似冲突 → 按新事实更新
)

// memoryCandidate 溯源数组的载体(解析与校验共用;M6 沉淀 schema 见 digest_reconcile.go)。
type memoryCandidate struct {
	SourceMessageIDs []int64
}

// validSourceIDs 溯源校验:只保留落在沉淀区间 (fromID,toID] 的消息 ID
// (越界丢弃——不可信的指针不如没有)。返回按序去重后的合法 ID。
func validSourceIDs(c memoryCandidate, fromID, toID int64) []int64 {
	seen := make(map[int64]bool, len(c.SourceMessageIDs))
	out := make([]int64, 0, len(c.SourceMessageIDs))
	for _, id := range c.SourceMessageIDs {
		if id > fromID && id <= toID && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// createAutoMemory 新增一条自动记忆(带分层 kind/挂靠 matter/溯源 links),失败仅记日志。
func (p *DigestPipeline) createAutoMemory(
	userID int64, content string, sourceMsgID *int64, validIDs []int64,
	vec []float32, model, kind string, matterID *int64, existing *[]*memorydomain.Memory,
) {
	m := memorydomain.NewAutoMemory(userID, content, sourceMsgID)
	m.Kind = memorydomain.NormalizeKind(kind)
	// M8.4 §14.2.3:episode 已断源,若 LLM 仍输出(旧上下文/漂移)显式 warn——可见,不静默降级
	if kind == memorydomain.MemoryKindEpisode {
		logger.WarnWithFields("memory: LLM 输出已断源的 episode kind,已归一为 fact",
			zap.Int64("user_id", userID), zap.String("content_prefix", content[:min(30, len(content))]))
	}
	m.MatterID = matterID
	if vec != nil {
		m.Embedding = vec
		m.EmbeddingModel = model
	}
	if err := p.memoryRepo.Create(m); err != nil {
		logMemoryWarn(userID, "自动记忆落库失败", m.ID, err)
		return
	}
	if err := p.memoryRepo.CreateLinks(memorydomain.NewMemoryMessageLinks(m.ID, validIDs)); err != nil {
		logMemoryWarn(userID, "记忆溯源映射写入失败", m.ID, err)
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
			return e.ID, candidateSkip // 重复提及:closed loop 也不恢复(§14.2.2 ①),重新托付走 loop_reopens
		}
		// closed loop 不得被冲突更新链改写(§14.2.2 ③):排除出更新候选
		if score >= conflictUpdateThreshold && score > updateScore && !e.IsClosedLoop() {
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
