package agentprompt

// 主 Agent 的 PromptRegistry 装配(11-Prompt管理 §5.1)。
// 主 Agent 提示词由命名 section 组成,经 PromptRegistry.Assemble 产出,与子 Agent 走同一机制。

// MainAgentSections 构造主 Agent 的全部 section(按 11-Prompt管理 §5.1)。
// hasSubAgents=false 时只含基础人格(agent_base),不含派活/汇报/任务管理——
// 语义上是"这些 section 在装配时不存在",替代旧的 hasSubAgents 布尔字符串拼接。
//
// section 文本来自 MainDelegationRulesPrompt 等常量(agent.go),常量块是唯一来源,
// 保证组装与顺序拼接逐字节一致(见 TestMainAgentMigration_Golden)。
func MainAgentSections(hasSubAgents bool) []PromptSection {
	sections := []PromptSection{
		// agent_base:基础人格(DefaultSystemPrompt),Order -100 最前。
		StaticSection("agent_base", ScopeMain, -100, DefaultSystemPrompt),
	}
	if !hasSubAgents {
		return sections
	}
	return append(sections,
		// 派活规则 → 汇报规则 → 任务管理,按 Order 升序(100/110/120)。
		StaticSection("delegation_rules", ScopeMain, 100, MainDelegationRulesPrompt),
		StaticSection("reporting_rules", ScopeMain, 110, MainReportingRulesPrompt),
		StaticSection("task_mgmt", ScopeMain, 120, MainTaskMgmtToolsPrompt),
	)
}

// BuildMainAgentSystemPrompt 用 registry 组装主 Agent 的 system prompt。
// hasSubAgents=false → 仅基础人格(等于 DefaultSystemPrompt)。
func BuildMainAgentSystemPrompt(hasSubAgents bool) (string, error) {
	r := NewPromptRegistry()
	for _, s := range MainAgentSections(hasSubAgents) {
		if err := r.Register(s); err != nil {
			return "", err
		}
	}
	return r.Assemble(PromptCtx{Scopes: []ScopeKey{ScopeMain}})
}