package agentprompt

import (
	"strings"
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
	want := DefaultSystemPrompt + MainDelegationRulesPrompt + MainReportingRulesPrompt + MainTaskMgmtToolsPrompt + MainSubscriptionRulesPrompt
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
	assert.True(t, sectionHas(withSub, ScopeMain, "subscription_rules"))

	noSub := MainAgentSections(false)
	assert.False(t, sectionHas(noSub, ScopeMain, "delegation_rules"), "无子 Agent 时不装配派活 section")
	assert.False(t, sectionHas(noSub, ScopeMain, "reporting_rules"))
	assert.False(t, sectionHas(noSub, ScopeMain, "task_mgmt"))
	assert.False(t, sectionHas(noSub, ScopeMain, "subscription_rules"))
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

// TestMainDelegationRulesPrompt_AntiHallucination 反幻觉铁律守护(2026-09-14 事故):
// 未调 delegate 却说"已安排(任务 #N)"的编造编号事故——铁律关键句不得在后续编辑中丢失。
func TestMainDelegationRulesPrompt_AntiHallucination(t *testing.T) {
	for _, want := range []string{
		"铁律",
		"只有当你本轮真实调用了 delegate 工具",
		"严禁编造",
		"推算",
		"沿用历史编号",
		"当场穿帮",
	} {
		assert.Contains(t, MainDelegationRulesPrompt, want, "派活规则缺少反幻觉关键句 %q", want)
	}
	// 铁律必须是规则体的第一段(优先级最高,不允许被其他段落稀释)
	first := strings.Index(MainDelegationRulesPrompt, "【铁律")
	second := strings.Index(MainDelegationRulesPrompt, "【什么时候派】")
	require.NotEqual(t, -1, first)
	require.NotEqual(t, -1, second)
	assert.Less(t, first, second, "铁律段必须位于「什么时候派」之前")
}

// TestMainDelegationRulesPrompt_ConciseHumanReply 派活后的回复必须是一句口语人话
// (task#20 反馈:模型把给子 Agent 的合同复述成三大节标题排版文,机械化没人味)。
func TestMainDelegationRulesPrompt_ConciseHumanReply(t *testing.T) {
	for _, want := range []string{
		"一句话",
		"不要复述",
		"短名",
	} {
		assert.Contains(t, MainDelegationRulesPrompt, want, "派活规则缺少关键句 %q", want)
	}
}
