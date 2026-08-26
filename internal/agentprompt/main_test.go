package agentprompt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 主 Agent prompt section 化的 golden 迁移测试(11-Prompt管理 §8)。
// 拆成 registry section 后,Assemble 输出必须与同一常量块顺序拼接逐字节一致(防组装回归)。

// TestMainAgentMigration_Golden_WithSubAgents hasSubAgents=true:组装输出 == 同一常量块顺序拼接。
// 锁定 registry 的排序/拼接/去副作用对同一来源常量吐出与朴素拼接一致的结果(防组装回归)。
func TestMainAgentMigration_Golden_WithSubAgents(t *testing.T) {
	got, err := BuildMainAgentSystemPrompt(true)
	require.NoError(t, err)
	want := DefaultSystemPrompt + MainDelegationRulesPrompt + MainReportingRulesPrompt + MainTaskMgmtToolsPrompt
	require.Equal(t, want, got,
		"主 Agent(有子 Agent)的 registry 组装必须与默认拼接逐字节一致")
}

// TestMainAgentMigration_Golden_NoSubAgents hasSubAgents=false:组装输出 == base prompt。
func TestMainAgentMigration_Golden_NoSubAgents(t *testing.T) {
	got, err := BuildMainAgentSystemPrompt(false)
	require.NoError(t, err)
	require.Equal(t, DefaultSystemPrompt, got, "无子 Agent 时只有基础人格")
}

// TestMainAgentSections_Scoping 派活/汇报/任务管理仅 hasSubAgents 时存在;基础人格恒在。
func TestMainAgentSections_Scoping(t *testing.T) {
	withSub := MainAgentSections(true)
	assert.True(t, sectionHas(withSub, ScopeMain, "delegation_rules"))
	assert.True(t, sectionHas(withSub, ScopeMain, "reporting_rules"))
	assert.True(t, sectionHas(withSub, ScopeMain, "task_mgmt"))

	noSub := MainAgentSections(false)
	assert.False(t, sectionHas(noSub, ScopeMain, "delegation_rules"), "无子 Agent 时不装配派活 section")
	assert.False(t, sectionHas(noSub, ScopeMain, "reporting_rules"))
	assert.False(t, sectionHas(noSub, ScopeMain, "task_mgmt"))
	assert.True(t, sectionHas(noSub, ScopeMain, "agent_base"), "基础人格恒在")
}

// TestMainAgentSections_Order 主 Agent sections 按 order 排序:base(-100) 在 delegation(100) 前。
func TestMainAgentSections_Order(t *testing.T) {
	sections := MainAgentSections(true)
	for i := 1; i < len(sections); i++ {
		assert.LessOrEqual(t, sections[i-1].Order, sections[i].Order,
			"sections 须按 Order 升序:base(-100) < delegation(100) < reporting(110) < task_mgmt(120)")
	}
}

func sectionHas(sections []PromptSection, scope ScopeKey, name string) bool {
	for _, s := range sections {
		if s.Scope == scope && s.Name == name {
			return true
		}
	}
	return false
}