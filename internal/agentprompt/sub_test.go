package agentprompt

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

// 子 Agent prompt section 化 + 去角色测试(11-Prompt管理 §8,08 §5.7)。
// 去角色后子 Agent 提示词 = 共享基础人格 + 通用执行器 persona + (可选)persona_hint + 任务合同,无角色卡模板。

// TestSubAgentPromptSections_NoRole 无 persona_hint:不含"研究员"等角色卡文案,含通用执行器 persona。
func TestSubAgentPromptSections_NoRole(t *testing.T) {
	s := SubAgentPromptSections(ScopeSub, domainagent.NewTaskSpec("查高铁票"))
	require.Len(t, s, 3) // agent_base + sub_role + sub_contract(无 hint 不加段)
	assert.Equal(t, "agent_base", s[0].Name)
	assert.Equal(t, "sub_role", s[1].Name)
	assert.Equal(t, "sub_contract", s[2].Name)
	assert.Less(t, s[0].Order, s[1].Order)
	assert.Less(t, s[1].Order, s[2].Order)
	assert.NotContains(t, s[1].Text, "研究员", "通用执行器 persona 不得含角色卡文案")
	assert.Contains(t, s[1].Text, "后台任务执行器")
}

// TestSubAgentPromptSections_PersonaHint 有 persona_hint:注入【本次任务角色】;空白 hint 不注册该段。
func TestSubAgentPromptSections_PersonaHint(t *testing.T) {
	// 空白 hint → 不注册 persona_hint 段
	s := SubAgentPromptSections(ScopeSub, domainagent.NewTaskSpec("x"))
	for _, sec := range s {
		assert.NotEqual(t, "sub_persona_hint", sec.Name)
	}

	// 非空 hint → 注册且含【本次任务角色】
	spec := domainagent.NewTaskSpec("x")
	spec.PersonaHint = "你是严谨的研究员,先多路检索再出结构化报告+来源"
	s = SubAgentPromptSections(ScopeSub, spec)
	var hint *PromptSection
	for i := range s {
		if s[i].Name == "sub_persona_hint" {
			hint = &s[i]
			break
		}
	}
	require.NotNil(t, hint, "非空 persona_hint 应注册 sub_persona_hint 段")
	assert.Contains(t, hint.Text, "【本次任务角色】")
	assert.Contains(t, hint.Text, "严谨的研究员")
	assert.Equal(t, 50, hint.Order, "persona_hint 在 persona 之后、合同之前")
}

// TestBuildSubAgentSystemPrompt_Universal 组装不含角色卡模板;基底 = 共享人格 + 通用 persona + 合同。
func TestBuildSubAgentSystemPrompt_Universal(t *testing.T) {
	spec := domainagent.NewTaskSpec("调研三框架")
	got, err := BuildSubAgentSystemPrompt(ScopeSub, spec)
	require.NoError(t, err)
	assert.Contains(t, got, "后台任务执行器")
	assert.Contains(t, got, "全平台智能助手")
	assert.NotContains(t, got, "研究员", "去角色后不得残留角色卡人设")
	require.NotContains(t, got, "{goal}") // {goal} 占位体系已由 contract 取代,不容忍残留
}

// TestBuildSubAgentSystemPrompt_Contract 完整详情仍注入任务合同(与去角色前一致的金丝雀)。
func TestBuildSubAgentSystemPrompt_Contract(t *testing.T) {
	spec := domainagent.NewTaskSpec("调研三框架")
	spec.Deliverables = []domainagent.Deliverable{{Name: "candidate_list", Description: "候选框架列表"}}
	spec.CompletionCriteria = []string{"比较三个", "给出推荐"}
	spec.Constraints = &domainagent.Constraints{MaxSteps: 15}
	got, err := BuildSubAgentSystemPrompt(ScopeSub, spec)
	require.NoError(t, err)
	assert.Contains(t, got, "== 任务合同(必须遵守)==")
	assert.Contains(t, got, "1. candidate_list: 候选框架列表")
	assert.Contains(t, got, "1. 比较三个")
	assert.Contains(t, got, "- 最大步数: 15")

	// 无详情 → 无合同段(用合同段头判定;persona 里"任务合同"字样不算)
	got2, _ := BuildSubAgentSystemPrompt(ScopeSub, domainagent.NewTaskSpec("仅目标"))
	assert.NotContains(t, got2, "== 任务合同(必须遵守)==")
}

// TestBuildSubAgentSystemPrompt_EmptyTime 含 deadline 约束仍注入(复用 buildSubContract)。
func TestBuildSubAgentSystemPrompt_Deadline(t *testing.T) {
	spec := domainagent.NewTaskSpec("x")
	spec.Constraints = &domainagent.Constraints{Deadline: time.Date(2026, 8, 28, 12, 0, 0, 0, time.Local)}
	got, err := BuildSubAgentSystemPrompt(ScopeSub, spec)
	require.NoError(t, err)
	assert.Contains(t, got, "- 截止时间: 2026-08-28 12:00")
}
