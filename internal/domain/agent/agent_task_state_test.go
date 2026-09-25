package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Task 状态机迁移矩阵测试(Phase 5,16-架构迭代路线图 §11/§12):
// 合法迁移表是唯一事实源,CAS 落库与服务层判断都以此为准。
//
// 与文档 §12.1 的差异:增加 pending → failed(启动阶段 panic 等异常落终态,
// 避免僵尸 pending)。其余与文档一致。

func TestCanTransition_Legal(t *testing.T) {
	legal := [][2]string{
		{TaskStatusPending, TaskStatusRunning},
		{TaskStatusPending, TaskStatusCancelled},
		{TaskStatusPending, TaskStatusFailed}, // 启动失败(panic-before-running)落终态
		{TaskStatusRunning, TaskStatusCompleted},
		{TaskStatusRunning, TaskStatusFailed},
		{TaskStatusRunning, TaskStatusInputRequired},
		{TaskStatusRunning, TaskStatusCancelled},
		{TaskStatusInputRequired, TaskStatusRunning},
		{TaskStatusInputRequired, TaskStatusCancelled},
	}
	for _, tr := range legal {
		assert.True(t, CanTransition(tr[0], tr[1]), "%s → %s 应为合法迁移", tr[0], tr[1])
	}
}

func TestCanTransition_Illegal(t *testing.T) {
	all := []string{TaskStatusPending, TaskStatusRunning, TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled, TaskStatusInputRequired}
	legal := map[[2]string]bool{
		{TaskStatusPending, TaskStatusRunning}:          true,
		{TaskStatusPending, TaskStatusCancelled}:        true,
		{TaskStatusPending, TaskStatusFailed}:           true,
		{TaskStatusRunning, TaskStatusCompleted}:        true,
		{TaskStatusRunning, TaskStatusFailed}:           true,
		{TaskStatusRunning, TaskStatusInputRequired}:    true,
		{TaskStatusRunning, TaskStatusCancelled}:        true,
		{TaskStatusInputRequired, TaskStatusRunning}:    true,
		{TaskStatusInputRequired, TaskStatusCancelled}:  true,
	}
	for _, from := range all {
		for _, to := range all {
			if legal[[2]string{from, to}] {
				continue
			}
			assert.False(t, CanTransition(from, to), "%s → %s 应为非法迁移", from, to)
		}
	}
}

// TestCanTransition_TerminalFrozen 终态不可迁出(核心正确性保证:
// cancelled 不可被 completed/failed 覆盖,completed 不可再取消)。
func TestCanTransition_TerminalFrozen(t *testing.T) {
	for _, terminal := range []string{TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled} {
		for _, to := range []string{TaskStatusPending, TaskStatusRunning, TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled, TaskStatusInputRequired} {
			assert.False(t, CanTransition(terminal, to), "终态 %s 不可迁移到 %s", terminal, to)
		}
	}
}
