package agent

// sub_agent_llm_adapter.go — 子 Agent 的按用户配置解析适配(2026-10 审计 #4 迁出 API 层)。

import userService "omnibot/internal/service/user"

// SubAgentLLMConfigAdapter 适配 userService.LLMConfigService → SubAgentLLMConfigProvider。
// 子 Agent 优先用用户自定义 LLM 配置,无配置时 runner 内部回落系统默认。
type SubAgentLLMConfigAdapter struct {
	Svc userService.LLMConfigService
}

func (a *SubAgentLLMConfigAdapter) GetFullConfig(userID int64) (apiKey, baseURL, model string, hasConfig bool, err error) {
	cfg, has, e := a.Svc.GetFullConfigForUser(userID)
	if e != nil {
		return "", "", "", false, e
	}
	if !has || cfg == nil {
		return "", "", "", false, nil
	}
	return cfg.APIKey, cfg.BaseURL, cfg.Model, true, nil
}
