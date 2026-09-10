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

// TestBuildTaskReceipt_IncludesContract 汇报锚定(§3.4 修订):回执带完整任务合同
// (交付物/完成标准),让主 Agent 能对照完成标准自查达标性,而非只转述结果。
func TestBuildTaskReceipt_IncludesContract(t *testing.T) {
	spec := domainagent.NewTaskSpec("查询北京天气")
	spec.Deliverables = []domainagent.Deliverable{
		{Name: "天气报告", Description: "当前气温+一周预报+穿衣建议"},
	}
	spec.CompletionCriteria = []string{"检索控制在3-5步", "直接输出完整可读结论"}
	spec.Background = map[string]any{"city": "北京"}
	task := domainagent.NewAgentTask(42, spec, "web", "")
	task.Status = domainagent.TaskStatusCompleted
	artifact := "北京今天晴,25度"
	task.Artifact = &artifact

	receipt := BuildTaskReceipt(task)
	for _, want := range []string{
		"目标: 查询北京天气",
		"交付物:",
		"- 天气报告: 当前气温+一周预报+穿衣建议",
		"完成标准:",
		"1. 检索控制在3-5步",
		"2. 直接输出完整可读结论",
	} {
		assert.Contains(t, receipt, want, "回执缺 %q:\n%s", want, receipt)
	}
}

// TestBuildTaskReceipt_MinimalSpecOmitsContract 只有 goal 的老合同不出现空段落。
func TestBuildTaskReceipt_MinimalSpecOmitsContract(t *testing.T) {
	task := domainagent.NewAgentTask(42, domainagent.NewTaskSpec("查天气"), "web", "")
	task.Status = domainagent.TaskStatusCompleted
	artifact := "结果"
	task.Artifact = &artifact

	receipt := BuildTaskReceipt(task)
	assert.NotContains(t, receipt, "交付物:")
	assert.NotContains(t, receipt, "完成标准:")
}

// TestBuildReportInstruction_SelfCheck 汇报指令要求对照完成标准自查,如实汇报不粉饰。
func TestBuildReportInstruction_SelfCheck(t *testing.T) {
	spec := domainagent.NewTaskSpec("查天气")
	spec.CompletionCriteria = []string{"输出完整可读结论"}
	task := domainagent.NewAgentTask(42, spec, "web", "")
	task.Status = domainagent.TaskStatusCompleted

	instruction := BuildReportInstruction([]*domainagent.AgentTask{task}, true)
	for _, want := range []string{"完成标准", "如实", "未达标"} {
		assert.Contains(t, instruction, want, "汇报指令缺 %q:\n%s", want, instruction)
	}
}
