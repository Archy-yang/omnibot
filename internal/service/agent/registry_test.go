package agent

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	domainagent "omnibot/internal/domain/agent"
)

func sampleCard(typ, name, desc string) domainagent.SubAgentCard {
	return domainagent.SubAgentCard{
		Type:           typ,
		Name:           name,
		Description:    desc,
		PromptTemplate: "你是一名" + name + "。目标:{goal}。",
		Tools:          []string{"rss_reader"},
		MaxSteps:       15,
		Timeout:        180 * time.Second,
	}
}

func TestSubAgentRegistry_RegisterAndGet(t *testing.T) {
	r := NewSubAgentRegistry()
	require.NoError(t, r.Register(sampleCard("researcher", "研究员", "查阅资料")))

	card, ok := r.Get("researcher")
	require.True(t, ok)
	assert.Equal(t, "研究员", card.Name)
	assert.Equal(t, "查阅资料", card.Description)
}

func TestSubAgentRegistry_GetNotFound(t *testing.T) {
	r := NewSubAgentRegistry()
	_, ok := r.Get("nonexistent")
	assert.False(t, ok)
}

func TestSubAgentRegistry_RegisterDuplicate(t *testing.T) {
	r := NewSubAgentRegistry()
	require.NoError(t, r.Register(sampleCard("researcher", "研究员", "查阅资料")))
	err := r.Register(sampleCard("researcher", "研究员2", "另一个"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestSubAgentRegistry_ListAll(t *testing.T) {
	r := NewSubAgentRegistry()
	require.NoError(t, r.Register(sampleCard("researcher", "研究员", "查阅资料")))
	require.NoError(t, r.Register(sampleCard("writer", "写手", "撰写内容")))

	all := r.ListAll()
	assert.Len(t, all, 2)
}

func TestSubAgentRegistry_DelegateToolDescription(t *testing.T) {
	r := NewSubAgentRegistry()
	// 空:返回占位文案
	assert.Contains(t, r.DelegateToolDescription(), "暂无可用子 Agent")

	require.NoError(t, r.Register(sampleCard("researcher", "研究员", "查阅资料任务")))
	desc := r.DelegateToolDescription()
	assert.Contains(t, desc, "researcher")
	assert.Contains(t, desc, "研究员")
	assert.Contains(t, desc, "查阅资料任务")
}
