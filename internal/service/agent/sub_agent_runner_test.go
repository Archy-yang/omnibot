package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	domainagent "omnibot/internal/domain/agent"
)

// TestBuildSubAgentPrompt_GoalOnly 仅 goal(无详情)等价于 ReplaceAll({goal})。
func TestBuildSubAgentPrompt_GoalOnly(t *testing.T) {
	got := buildSubAgentPrompt("你是研究员。目标:{goal}", domainagent.NewTaskSpec("研究 X"))
	assert.Contains(t, got, "研究 X")
	assert.NotContains(t, got, "任务合同", "无详情不应注入任务合同段")
}

// TestBuildSubAgentPrompt_WithDeliverables 有 deliverables/criteria 应注入任务合同段。
func TestBuildSubAgentPrompt_WithDeliverables(t *testing.T) {
	spec := domainagent.TaskSpec{
		Goal: "研究 Go 框架",
		Deliverables: []domainagent.Deliverable{
			{Name: "candidate_list", Description: "候选框架列表"},
			{Name: "recommendation", Description: "推荐顺序和理由"},
		},
		CompletionCriteria: []string{"至少比较三个框架", "给出明确推荐"},
		Background:         map[string]any{"project": "永久助理"},
		Constraints:        &domainagent.Constraints{MaxSteps: 10},
	}
	got := buildSubAgentPrompt("你是研究员。目标:{goal}", spec)

	assert.Contains(t, got, "研究 Go 框架")
	assert.Contains(t, got, "任务合同")
	assert.Contains(t, got, "必须交付")
	assert.Contains(t, got, "candidate_list")
	assert.Contains(t, got, "recommendation")
	assert.Contains(t, got, "完成标准")
	assert.Contains(t, got, "至少比较三个框架")
	assert.Contains(t, got, "给出明确推荐")
	assert.Contains(t, got, "背景")
	assert.Contains(t, got, "project: 永久助理")
	assert.Contains(t, got, "约束")
	assert.Contains(t, got, "最大步数: 10")
	assert.Contains(t, got, "满足完成标准后必须立即产出报告")
}

// TestBuildSubAgentPrompt_PromptWithoutGoalTag 模板无 {goal} 占位时,goal 不注入(防御)。
func TestBuildSubAgentPrompt_PromptWithoutGoalTag(t *testing.T) {
	got := buildSubAgentPrompt("你是研究员。", domainagent.NewTaskSpec("X"))
	// 无 {goal} 占位,goal 不替换,但 prompt 原样返回
	assert.Contains(t, got, "你是研究员")
	assert.False(t, strings.Contains(got, "X"))
}
