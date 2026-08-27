package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	domainagent "omnibot/internal/domain/agent"
)

// 回执测试(去角色后不再依赖 registry;SubAgentType 作溯源标签显示)。

// TestBuildTaskReceipt_Completed 完成任务回执含任务ID/类型标签(SubAgentType)/目标/结果摘要。
func TestBuildTaskReceipt_Completed(t *testing.T) {
	artifact := "Go 1.24 要点"
	task := &domainagent.AgentTask{
		ID: 123, UserID: 1, SubAgentType: "research", Goal: "研究 Go 1.24",
		Status: domainagent.TaskStatusCompleted, Artifact: &artifact,
	}

	got := BuildTaskReceipt(task)
	assert.Contains(t, got, "任务ID: 123")
	assert.Contains(t, got, "任务类型: research")
	assert.Contains(t, got, "研究 Go 1.24")
	assert.Contains(t, got, "Go 1.24 要点")
}

// TestBuildTaskReceipt_NoTypeLabel 无 SubAgentType 标签时显示"后台任务"。
func TestBuildTaskReceipt_NoTypeLabel(t *testing.T) {
	artifact := "r"
	task := &domainagent.AgentTask{
		ID: 1, Goal: "g",
		Status: domainagent.TaskStatusCompleted, Artifact: &artifact,
	}
	got := BuildTaskReceipt(task)
	assert.Contains(t, got, "任务类型: 后台任务")
}

func TestBuildTaskReceipt_Failed(t *testing.T) {
	errMsg := "子 Agent 执行超时"
	task := &domainagent.AgentTask{
		ID: 456, UserID: 1, SubAgentType: "research", Goal: "g",
		Status: domainagent.TaskStatusFailed, ErrorMsg: &errMsg,
	}

	got := BuildTaskReceipt(task)
	assert.Contains(t, got, "失败: 子 Agent 执行超时")
	assert.NotContains(t, got, "结果:\n\n") // 不应有空结果
}

func TestBuildReportInstruction_MultipleTasks(t *testing.T) {
	a1 := "r1"
	a2 := "r2"
	tasks := []*domainagent.AgentTask{
		{ID: 1, SubAgentType: "research", Goal: "g1", Status: domainagent.TaskStatusCompleted, Artifact: &a1},
		{ID: 2, SubAgentType: "research", Goal: "g2", Status: domainagent.TaskStatusCompleted, Artifact: &a2},
	}
	got := BuildReportInstruction(tasks, true)
	assert.Contains(t, got, "管家口吻")
	assert.Contains(t, got, "任务ID: 1")
	assert.Contains(t, got, "任务ID: 2")
	assert.Contains(t, got, "r1")
	assert.Contains(t, got, "r2")
}

// TestBuildTaskReceipt_SummaryTruncates 控制面/数据面分离(#20):
// 前置汇报回执(fullArtifact=false)只给摘要(截断),不塞全文。
func TestBuildTaskReceipt_SummaryTruncates(t *testing.T) {
	longArtifact := strings.Repeat("详", 300) // 超过 receiptSummaryMax(200)
	task := &domainagent.AgentTask{
		ID: 1, SubAgentType: "research", Goal: "g",
		Status: domainagent.TaskStatusCompleted, Artifact: &longArtifact,
	}

	// 摘要模式(前置汇报):截断 + 提示
	summary := BuildTaskReceipt(task)
	assert.Contains(t, summary, "...")
	assert.Contains(t, summary, "完整结果可查任务产物")
	assert.False(t, strings.Contains(summary, strings.Repeat("详", 201)),
		"摘要不应含超长全文")

	// 完整模式(report 接口):不截断,给全文
	full := buildTaskReceiptForReport(task, true)
	assert.NotContains(t, full, "完整结果可查任务产物")
	assert.Contains(t, full, strings.Repeat("详", 300))
}
