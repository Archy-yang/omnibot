package agentprompt

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// 子 Agent prompt section 化的 golden 迁移测试(11-Prompt管理 §8)。

const testSubTemplate = `你是一名研究员。目标:{goal}。

工作方式:用可用工具检索信息,最后产出一份结构化报告。`

// TestSubAgentMigration_Golden_GoalOnly 仅 goal(无详情):registry 组装 == role(替换 goal) + 空合同。
func TestSubAgentMigration_Golden_GoalOnly(t *testing.T) {
	spec := domainagent.NewTaskSpec("查高铁票")
	got, err := BuildSubAgentSystemPrompt(SubScope("researcher"), testSubTemplate, spec)
	require.NoError(t, err)
	want := strings.ReplaceAll(testSubTemplate, "{goal}", spec.Goal) + buildSubContract(spec)
	require.Equal(t, want, got, "子 Agent 仅 goal 时 registry 组装 == role段 + 空合同段")
	require.Contains(t, got, "目标:查高铁票")
	require.NotContains(t, got, "任务合同", "无详情时不出现任务合同段")
}

// TestSubAgentMigration_Golden_FullDetail 完整详情(deliverables/criteria/background/constraints)。
func TestSubAgentMigration_Golden_FullDetail(t *testing.T) {
	spec := domainagent.NewTaskSpec("调研三框架")
	spec.Deliverables = []domainagent.Deliverable{
		{Name: "candidate_list", Description: "候选框架列表"},
	}
	spec.CompletionCriteria = []string{"比较三个", "给出推荐"}
	spec.Background = map[string]any{"stack": "Go+Gin"}
	spec.Constraints = &domainagent.Constraints{
		MaxSteps:      15,
		MaxToolCalls:  20,
		Deadline:      time.Date(2026, 8, 28, 12, 0, 0, 0, time.Local),
	}

	got, err := BuildSubAgentSystemPrompt(SubScope("researcher"), testSubTemplate, spec)
	require.NoError(t, err)
	// golden 锁定:registry 组装 == role(替换 goal) + buildSubContract(任务合同),role 段在 contract 段前。
	want := strings.ReplaceAll(testSubTemplate, "{goal}", spec.Goal) + buildSubContract(spec)
	require.Equal(t, want, got, "子 Agent 含任务合同时 registry 组装 == role段 + 合同段")

	assert.Contains(t, got, "== 任务合同(必须遵守)==")
	assert.Contains(t, got, "1. candidate_list: 候选框架列表")
	assert.Contains(t, got, "1. 比较三个")
	assert.Contains(t, got, "- stack: Go+Gin")
	assert.Contains(t, got, "- 最大步数: 15")
	assert.Contains(t, got, "- 截止时间: 2026-08-28 12:00")
}

// TestSubAgentPromptSections_Order 子 Agent sections 按 order:role(0) 在 contract(100) 前。
func TestSubAgentPromptSections_Order(t *testing.T) {
	s := SubAgentPromptSections(SubScope("researcher"), testSubTemplate, domainagent.NewTaskSpec("x"))
	require.Len(t, s, 2)
	assert.Equal(t, "sub_role", s[0].Name)
	assert.Equal(t, "sub_contract", s[1].Name)
	assert.Less(t, s[0].Order, s[1].Order)
}