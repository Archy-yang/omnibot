package chat

import (
	"context"
	"fmt"

	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// ContextCompactor History Compact 的压缩器抽象(Phase 2,16-架构迭代路线图 §7)。
// 生产实现走 LLM(llmContextCompactor);测试用桩。
type ContextCompactor interface {
	// Compact 基于旧 Compact + 被压缩区间的原始消息文本,产出合并后的新 Compact。
	// oldCompact 为空表示首次压缩。失败返回错误,调用方降级(水位不推进,下轮重试)。
	Compact(ctx context.Context, userID int64, oldCompact string, rawTexts []string) (string, error)
}

// Context 预算常量(§7.4)。按 128K 上下文模型设定,单机个人部署不做运行时配置。
const (
	// DefaultKeepRecentTokens 尾窗预算:Recent Raw 保留的估算 token 上限。
	DefaultKeepRecentTokens = 15000
	// DefaultCompactTriggerTokens 中段积压触发阈值:水位与尾窗之间的未压缩原文
	// 估算 token 超过该值即触发一次 LLM 压缩(§7.4 按量触发,非按轮数)。
	DefaultCompactTriggerTokens = 20000
	// maxContextScanMessages 单次构建最多扫描的消息数(查询上限,防历史极大时全表拖拽;
	// 中段超出部分留待下一轮压缩分批消化)。
	maxContextScanMessages = 400
)

// loadContextState 取用户的对话与 Compact 状态。
// 返回 conversationID(0=无对话,跳过压缩)与状态(nil=从未压缩)。
func (s *messageService) loadContextState(ctx context.Context, userID int64) (int64, *conversation.ConversationContextState) {
	if s.convRepo == nil || s.contextStateRepo == nil {
		return 0, nil
	}
	ag, err := s.convRepo.GetAgentByCode(agentdomain.AgentCodeMain)
	if err != nil {
		return 0, nil
	}
	conv, err := s.convRepo.GetActiveByUserAndAgent(ctx, userID, ag.ID)
	if err != nil || conv == nil {
		return 0, nil
	}
	state, err := s.contextStateRepo.GetByConversationID(conv.ID)
	if err != nil {
		return 0, nil
	}
	return conv.ID, state
}

// buildRecentRaw 产出 Recent Raw 原文(时间正序)与生效的 Compact 文本(§7.1/§7.4):
//  1. 候选 = compact 水位之后的全部消息(倒序扫描,上限 maxContextScanMessages);
//  2. 尾窗:从最新往前按 keepRecentTokens 预算截取,预算内一条不丢;
//  3. 中段(水位与尾窗之间)积压 ≥ compactTriggerTokens 且有压缩器 → LLM 压缩,
//     水位推进到中段末尾;失败则降级(本轮不注入,水位不动,下轮重试)。
func (s *messageService) buildRecentRaw(ctx context.Context, userID int64) ([]*conversation.Message, string) {
	convID, state := s.loadContextState(ctx, userID)
	compactUntil := int64(0)
	compactText := ""
	if state != nil {
		compactUntil = state.CompactUntilMessageID
		if state.CompactContent != nil {
			compactText = *state.CompactContent
		}
	}

	candidates, err := s.msgRepo.GetRecentByUserIDAfter(userID, compactUntil, maxContextScanMessages)
	if err != nil {
		return nil, compactText
	}
	if len(candidates) == 0 {
		return nil, compactText
	}

	// 尾窗:预算内从最新往旧尽量多留(§6.1:Turn 数/条数都不代表 Context 大小)
	oldestKept := len(candidates) // 默认一条不留
	used := 0
	for i := len(candidates) - 1; i >= 0; i-- {
		t := EstimateTokens(candidates[i].Content)
		if used+t > s.keepRecentTokens {
			break
		}
		used += t
		oldestKept = i
	}
	// 兜底:单条消息超预算也至少保留最新一条(绝不返回空 Recent)
	if oldestKept == len(candidates) {
		oldestKept = len(candidates) - 1
	}

	// 中段积压压缩(§7.4)
	middle := candidates[:oldestKept]
	if len(middle) > 0 && s.compactor != nil && convID > 0 {
		middleTokens := 0
		for _, m := range middle {
			middleTokens += EstimateTokens(m.Content)
		}
		if middleTokens >= s.compactTriggerTokens {
			texts := make([]string, 0, len(middle))
			for _, m := range middle {
				texts = append(texts, formatMessageForCompact(m))
			}
			newCompact, cerr := s.compactor.Compact(ctx, userID, compactText, texts)
			if cerr != nil {
				// 降级:水位不推进,下轮重试;本轮上下文照常用尾窗
				logger.ErrorWithFields("compact: LLM 压缩失败,水位不推进(下轮重试)",
					zap.Int64("user_id", userID),
					zap.Int("middle_messages", len(middle)),
					zap.Error(cerr),
				)
				return candidates[oldestKept:], compactText
			}
			compactText = newCompact
			logger.InfoWithFields("compact: 压缩完成,水位推进",
				zap.Int64("user_id", userID),
				zap.Int("middle_messages", len(middle)),
				zap.Int("middle_tokens", middleTokens),
				zap.Int("compact_tokens", EstimateTokens(newCompact)),
			)
			s.saveCompactState(ctx, convID, state, middle[len(middle)-1].ID, newCompact)
		}
	}

	return candidates[oldestKept:], compactText
}

// saveCompactState 推进 Compact 水位(§7.3:Upsert 单行状态)。
func (s *messageService) saveCompactState(ctx context.Context, conversationID int64, prev *conversation.ConversationContextState, untilMessageID int64, newCompact string) {
	st := &conversation.ConversationContextState{
		ConversationID:        conversationID,
		CompactContent:        &newCompact,
		CompactUntilMessageID: untilMessageID,
		CompactVersion:        1,
		CompactCount:          1,
		TokenCount:            EstimateTokens(newCompact),
	}
	if prev != nil {
		st.CompactVersion = prev.CompactVersion + 1
		st.CompactCount = prev.CompactCount + 1
	}
	if err := s.contextStateRepo.Upsert(st); err != nil {
		// 水位写失败 = 本轮压缩白做但不致错;下轮中段仍在会再次触发(幂等重试)
		logger.ErrorWithFields("compact: 水位写库失败,下轮将重新压缩",
			zap.Int64("conversation_id", conversationID),
			zap.Error(err),
		)
	}
}

// formatMessageForCompact 把消息格式化为压缩输入原文(带角色标注;report 有专属标注,
// §7.6 验收要求 Compact 前后的 Task Report 不丢失)。
func formatMessageForCompact(m *conversation.Message) string {
	role := m.Role
	if m.Kind == conversation.KindReport {
		role = "assistant(子任务汇报)"
	}
	return fmt.Sprintf("[%s] %s", role, m.Content)
}
