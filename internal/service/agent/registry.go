package agent

import (
	"fmt"
	"strings"

	domainagent "omnibot/internal/domain/agent"
)

// SubAgentRegistry 子 Agent 注册中心(08 技术方案 §4.3)。
// 注册/查询 SubAgentCard。主 Agent delegate 工具描述据此动态生成。
type SubAgentRegistry struct {
	cards map[string]domainagent.SubAgentCard
}

// NewSubAgentRegistry 创建子 Agent 注册中心
func NewSubAgentRegistry() *SubAgentRegistry {
	return &SubAgentRegistry{
		cards: make(map[string]domainagent.SubAgentCard),
	}
}

// Register 注册子 Agent Card,Type 重复返回错误。
func (r *SubAgentRegistry) Register(card domainagent.SubAgentCard) error {
	if _, exists := r.cards[card.Type]; exists {
		return fmt.Errorf("sub agent %q already registered", card.Type)
	}
	r.cards[card.Type] = card
	return nil
}

// Get 按 Type 查询子 Agent Card。
func (r *SubAgentRegistry) Get(subAgentType string) (domainagent.SubAgentCard, bool) {
	card, ok := r.cards[subAgentType]
	return card, ok
}

// ListAll 列出所有已注册子 Agent Card(无序)。
func (r *SubAgentRegistry) ListAll() []domainagent.SubAgentCard {
	result := make([]domainagent.SubAgentCard, 0, len(r.cards))
	for _, card := range r.cards {
		result = append(result, card)
	}
	return result
}

// DelegateToolDescription 生成 delegate 工具描述里"可用子 Agent"的文案,
// 供主 Agent LLM 据此决定派活。格式:
//
//	- researcher(研究员): 用于需要查阅资料...
//	- <type>(<name>): <description>
func (r *SubAgentRegistry) DelegateToolDescription() string {
	if len(r.cards) == 0 {
		return "（暂无可用子 Agent）"
	}
	var b strings.Builder
	for _, card := range r.cards {
		fmt.Fprintf(&b, "- %s(%s): %s\n", card.Type, card.Name, card.Description)
	}
	return b.String()
}
