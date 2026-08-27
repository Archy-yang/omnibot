package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad_AgentSubAgentAllowedCapabilities config 新增的 agent.sub_agent.allowed_capabilities 正确解析。
func TestLoad_AgentSubAgentAllowedCapabilities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
agent:
  sub_agent:
    allowed_capabilities:
      - research
      - interactive
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"research", "interactive"}, cfg.Agent.SubAgent.AllowedCapabilities)
}

// TestLoad_AgentSectionAbsent 旧配置无 agent 段:不报错,AllowedCapabilities 为空(装配点回落默认)。
func TestLoad_AgentSectionAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app:\n  name: \"omnibot\"\n  port: 8080\n"), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Agent.SubAgent.AllowedCapabilities, 0)
}
