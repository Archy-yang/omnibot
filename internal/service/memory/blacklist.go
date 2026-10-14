package memory

import (
	"errors"
	"strings"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 沉淀黑名单(22号 §5):用户禁止长期沉淀的关键词/短语。
// 三处拦截:沉淀管线 skip / 注入不带 / 检索不返回;Journal 管理视图不过滤(可见真相)。
// 建规则即删存量并回报条数("以后别记 + 已有的忘掉")。

const blacklistPatternMaxRunes = 128

var ErrBlacklistInvalidPattern = errors.New("blacklist: pattern 不能为空且不超过 128 字")

// SetBlacklistRepo 注入黑名单仓储(可选增强;nil = 黑名单整体禁用,向后兼容)。
func (s *memoryService) SetBlacklistRepo(r memoryrepo.BlacklistRepository) {
	s.blacklistRepo = r
}

// containsBlacklisted 判断内容是否命中黑名单(大小写不敏感包含匹配)。
func containsBlacklisted(content string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	lowered := strings.ToLower(content)
	for _, p := range patterns {
		if p != "" && strings.Contains(lowered, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

// listPatterns 规则集(nil repo → 空,黑名单禁用)。
func (s *memoryService) listPatterns(userID int64) []string {
	if s.blacklistRepo == nil {
		return nil
	}
	patterns, err := s.repo.ListBlacklistPatterns(userID)
	if err != nil {
		logger.WarnWithFields("memory: 黑名单规则读取失败,本轮不过滤",
			zap.Int64("user_id", userID), zap.Error(err))
		return nil
	}
	return patterns
}

// ListBlacklist 规则清单。
func (s *memoryService) ListBlacklist(userID int64) ([]*memorydomain.MemoryBlacklist, error) {
	if s.blacklistRepo == nil {
		return []*memorydomain.MemoryBlacklist{}, nil
	}
	return s.blacklistRepo.ListByUserID(userID)
}

// AddBlacklist 新增规则 + 删除存量匹配记忆。返回(规则, 删除条数, err)。
func (s *memoryService) AddBlacklist(userID int64, pattern string) (*memorydomain.MemoryBlacklist, int64, error) {
	if s.blacklistRepo == nil {
		return nil, 0, ErrBlacklistInvalidPattern
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || utf8.RuneCountInString(pattern) > blacklistPatternMaxRunes {
		return nil, 0, ErrBlacklistInvalidPattern
	}
	rule, err := s.blacklistRepo.Create(userID, pattern)
	if err != nil {
		return nil, 0, err
	}
	deleted, err := s.blacklistRepo.DeleteMatchingMemories(userID, pattern)
	if err != nil {
		// 规则已生效(拦截面在工作),存量删除失败只记日志——下轮注入/检索仍会过滤
		logger.WarnWithFields("memory: 黑名单存量清理失败(拦截面不受影响)",
			zap.Int64("user_id", userID), zap.String("pattern", pattern), zap.Error(err))
	}
	logger.InfoWithFields("memory: 黑名单规则新增",
		zap.Int64("user_id", userID), zap.String("pattern", pattern), zap.Int64("deleted", deleted))
	return rule, deleted, nil
}

// RemoveBlacklist 解除规则(已删存量不恢复——语义是"以后可以记")。返回是否命中。
func (s *memoryService) RemoveBlacklist(userID, id int64) (bool, error) {
	if s.blacklistRepo == nil {
		return false, nil
	}
	return s.blacklistRepo.Delete(userID, id)
}

// filterByBlacklist 通用过滤:返回未命中黑名单的下标子集(注入侧用)。
func (s *memoryService) filterByBlacklist(userID int64, contents []string) []string {
	patterns := s.listPatterns(userID)
	if len(patterns) == 0 {
		return contents
	}
	out := make([]string, 0, len(contents))
	for _, c := range contents {
		if !containsBlacklisted(c, patterns) {
			out = append(out, c)
		}
	}
	return out
}
