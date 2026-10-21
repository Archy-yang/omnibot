package memory

// 风格注入(M2,24号 §6 修订版):把最近的原始风格观察拼成注入段,由 chat 层
// BuildContextMessages 作为 system 消息注入(与记忆常驻块/召回块同一 per-user 通道)。
//
// 红线(20号 §8 + 24号):只注原始观察、无 LLM 聚合环节;开关默认关;
// 关 = 不追加任何内容,完全回通用 persona(可即时回滚,无残留)。
// 原案(24号 §6)的 per-user prompt decorator 前提已被 M8.3 记忆常驻块推翻——
// 该通道天然 per-user、每轮现查,无缓存失效问题(修订记录见 24号文档 §6)。

import (
	"context"
	"errors"
	"strings"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// errStyleStoreMissing M2 未装配开关存取(装配点漏注)时的显式错误。
var errStyleStoreMissing = errors.New("style: 注入开关存取未装配")

const (
	// styleSegmentMaxObservations 注入条数上限(24号 §6:最近 ≤10 条)。
	styleSegmentMaxObservations = 10
	// styleSegmentMaxItemLen 单条观察截断(runes)——观察文本长度不可控(管线抽取),防单条刷屏。
	styleSegmentMaxItemLen = 120
	// styleSegmentMaxTotalLen 段落总长上限(runes),超出停止追加(兜底预算)。
	styleSegmentMaxTotalLen = 1500
)

// StyleInjectionStore 风格注入开关存取(窄接口;wire 注入 user repo,实现见 GormUserRepository)。
type StyleInjectionStore interface {
	GetStyleInjectionEnabled(userID int64) (bool, error)
	SetStyleInjectionEnabled(userID int64, enabled bool) error
}

// SetStyleInjectionStore 注入开关存取(装配点调用;nil 安全)。
func (s *memoryService) SetStyleInjectionStore(store StyleInjectionStore) {
	s.styleStore = store
}

// GetStyleInjection 读开关态(设置 API)。
func (s *memoryService) GetStyleInjection(userID int64) (bool, error) {
	if s.styleStore == nil {
		return false, nil
	}
	return s.styleStore.GetStyleInjectionEnabled(userID)
}

// SetStyleInjection 写开关态(设置 API;归 user 表,auth 归属由 handler 保证)。
func (s *memoryService) SetStyleInjection(userID int64, enabled bool) error {
	if s.styleStore == nil {
		return errStyleStoreMissing
	}
	return s.styleStore.SetStyleInjectionEnabled(userID, enabled)
}

// StylePromptSegment 组装风格注入段。开关关/无观察/读失败 → 空串(chat 层不追加)。
// 读失败静默降级(与记忆注入同一纪律:证据增强不阻塞对话)。
func (s *memoryService) StylePromptSegment(ctx context.Context, userID int64) string {
	if s.styleStore == nil {
		return ""
	}
	enabled, err := s.styleStore.GetStyleInjectionEnabled(userID)
	if err != nil {
		logger.ErrorWithFields("style: 注入开关读取失败,本轮降级不注入",
			zap.Int64("user_id", userID), zap.Error(err))
		return ""
	}
	if !enabled {
		return ""
	}
	obs, err := s.repo.ListStyleObservations(userID, styleSegmentMaxObservations)
	if err != nil {
		logger.ErrorWithFields("style: 观察读取失败,本轮降级不注入",
			zap.Int64("user_id", userID), zap.Error(err))
		return ""
	}
	if len(obs) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("== 用户的沟通风格(参考) ==\n以下是对该用户近期表达方式的观察,回复时自然参考,不要机械执行:\n")
	total := utf8RuneCount(sb.String())
	for _, o := range obs {
		item := truncateRunes(o.Content, styleSegmentMaxItemLen)
		line := "- [" + o.Dimension + "] " + item + "\n"
		cost := utf8RuneCount(line)
		if total+cost > styleSegmentMaxTotalLen {
			break
		}
		sb.WriteString(line)
		total += cost
	}
	return strings.TrimRight(sb.String(), "\n")
}

// utf8RuneCount runes 计数(中文按字算预算)。
func utf8RuneCount(s string) int { return len([]rune(s)) }

// truncateRunes 超长截断并以省略号收尾。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
