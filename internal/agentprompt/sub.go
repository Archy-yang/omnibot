package agentprompt

import (
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// 子 Agent 的 PromptRegistry 装配(11-Prompt管理 §5.2)。
// 子 Agent 提示词 = 角色模板(含 {goal},由本次任务 goal 填入) + 任务合同(deliverables/criteria)。
// 与主 Agent 走同一 registry 组装机制。

// buildSubContract 生成任务合同段(含前导空行,跟在角色模板后)。spec 无详情时返回空串。
// 抽取自原 buildSubAgentPrompt 的详情注入逻辑,单一来源。
func buildSubContract(spec domainagent.TaskSpec) string {
	if !spec.HasDetail() {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n== 任务合同(必须遵守)==\n")
	if len(spec.Deliverables) > 0 {
		b.WriteString("\n【必须交付】\n")
		for i, d := range spec.Deliverables {
			b.WriteString(fmt.Sprintf("%d. %s: %s\n", i+1, d.Name, d.Description))
		}
	}
	if len(spec.CompletionCriteria) > 0 {
		b.WriteString("\n【完成标准(全部满足才算完成,达成后立即产出报告,不要继续检索)】\n")
		for i, c := range spec.CompletionCriteria {
			b.WriteString(fmt.Sprintf("%d. %s\n", i+1, c))
		}
	}
	if len(spec.Background) > 0 {
		b.WriteString("\n【背景】\n")
		for k, v := range spec.Background {
			b.WriteString(fmt.Sprintf("- %s: %v\n", k, v))
		}
	}
	if spec.Constraints != nil {
		b.WriteString("\n【约束】\n")
		if spec.Constraints.MaxSteps > 0 {
			b.WriteString(fmt.Sprintf("- 最大步数: %d\n", spec.Constraints.MaxSteps))
		}
		if spec.Constraints.MaxToolCalls > 0 {
			b.WriteString(fmt.Sprintf("- 最大工具调用次数: %d\n", spec.Constraints.MaxToolCalls))
		}
		if !spec.Constraints.Deadline.IsZero() {
			b.WriteString(fmt.Sprintf("- 截止时间: %s\n", spec.Constraints.Deadline.Format("2006-01-02 15:04")))
		}
	}
	b.WriteString("\n注意:满足完成标准后必须立即产出报告,不要继续无意义检索。")
	return b.String()
}

// SubAgentPromptSections 生成子 Agent 的 sections(§5.2):
//   - sub_role(0):角色模板,{goal} 已由本次任务 goal 填入
//   - sub_contract(100):任务合同(spec 无详情时为空文本,组装时跳过)
func SubAgentPromptSections(scope ScopeKey, template string, spec domainagent.TaskSpec) []PromptSection {
	return []PromptSection{
		StaticSection("sub_role", scope, 0, strings.ReplaceAll(template, "{goal}", spec.Goal)),
		StaticSection("sub_contract", scope, 100, buildSubContract(spec)),
	}
}

// BuildSubAgentSystemPrompt 用 registry 组装子 Agent 的 system prompt。
func BuildSubAgentSystemPrompt(scope ScopeKey, template string, spec domainagent.TaskSpec) (string, error) {
	r := NewPromptRegistry()
	for _, s := range SubAgentPromptSections(scope, template, spec) {
		if err := r.Register(s); err != nil {
			return "", err
		}
	}
	return r.Assemble(PromptCtx{Scopes: []ScopeKey{scope}})
}