package memory

// 目标闭环——收口服务(28号 N1,方案 §3.2)。
// 对话内收口出口:manage_matter(close/archive/reopen) + close_loop(关键词匹配)。
// 红线(28号 §2):收口动作只能由用户对话确认触发(经 agent 工具),系统侧(管线/调度器)
// 严禁调用本文件——误判"已有结论"的代价是丢失跟进。

import (
	"context"
	"errors"
	"strings"

	memorydomain "omnibot/internal/domain/memory"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// settleActions 对话内合法的档案状态转移(28号 §3.2;非法 action 显性报错)。
// ErrMatterNotFound 复用 journal.go 既有哨兵(归属隔离同语义)。
var settleActions = map[string]string{
	"close":   memorydomain.MatterStatusDone,    // 完结:级联关闭其下 open loops
	"archive": memorydomain.MatterStatusArchived, // 搁置:不级联(可能回头)
	"reopen":  memorydomain.MatterStatusActive,   // 重开(反悔路径)
}

// MatterSettleResult 收口结果(工具文案拼装用)。
type MatterSettleResult struct {
	Title         string
	Status        string // 最终状态(active/done/archived)
	CascadedLoops int    // close 时联动关闭的 open loops 数
}

// SettleMatter 档案状态转移。close → done 并级联关闭其下 open loops;
// archive/reopen 不级联。未命中(不存在/他人)返回 ErrMatterNotFound。
func (s *memoryService) SettleMatter(ctx context.Context, userID, matterID int64, action string) (MatterSettleResult, error) {
	status, ok := settleActions[action]
	if !ok {
		return MatterSettleResult{}, errors.New("memory: 非法的收口动作(仅 close/archive/reopen)")
	}
	m, err := s.matterRepo.GetByID(userID, matterID)
	if err != nil {
		return MatterSettleResult{}, err
	}
	if m == nil {
		return MatterSettleResult{}, ErrMatterNotFound
	}
	okUp, err := s.matterRepo.UpdateFields(userID, matterID, nil, &status)
	if err != nil {
		return MatterSettleResult{}, err
	}
	if !okUp {
		return MatterSettleResult{}, ErrMatterNotFound
	}

	res := MatterSettleResult{Title: m.Title, Status: status}
	if action == "close" {
		n, err := s.repo.CloseOpenLoopsByMatter(userID, matterID)
		if err != nil {
			// 状态已转移,级联失败只影响待办钩子残留(管理面可收),不回滚主动作
			logger.ErrorWithFields("settle: 档案级联关闭 loops 失败",
				zap.Int64("user_id", userID), zap.Int64("matter_id", matterID), zap.Error(err))
		}
		res.CascadedLoops = int(n)
	}
	return res, nil
}

// CloseLoopByQuery 按关键词收口 open loop(孤儿 loop——无 matter 挂靠——的唯一对话内出口)。
// 子串匹配该用户全部 open loops:唯一命中 → 关闭(closed=true);
// 多命中 → 返回候选内容列表(closed=false),由模型让用户确认后用更具体关键词重试;
// 零命中 → 空列表(closed=false)。
func (s *memoryService) CloseLoopByQuery(ctx context.Context, userID int64, query string) ([]string, bool, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, false, errors.New("memory: 需要待办关键词")
	}
	loops, err := s.repo.ListOpenLoops(userID, 100)
	if err != nil {
		return nil, false, err
	}
	var matches []*memorydomain.Memory
	for _, l := range loops {
		if strings.Contains(l.Content, q) {
			matches = append(matches, l)
		}
	}
	switch {
	case len(matches) == 0:
		return nil, false, nil
	case len(matches) > 1:
		cands := make([]string, 0, len(matches))
		for _, m := range matches {
			cands = append(cands, m.Content)
		}
		return cands, false, nil
	}
	ok, err := s.repo.TransitionLoopStatus(matches[0].ID, userID, memorydomain.MemoryLoopStatusOpen, memorydomain.MemoryLoopStatusClosed)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil // CAS 未命中(并发已关):按零命中语义沉默
	}
	return nil, true, nil
}
