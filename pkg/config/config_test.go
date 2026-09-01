package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestLoad_AgentSubAgentAllowedCapabilities config 新增的 agent.sub_agent.allowed_capabilities/timeout 正确解析。
func TestLoad_AgentSubAgentAllowedCapabilities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
agent:
  sub_agent:
    allowed_capabilities:
      - research
      - interactive
    timeout: "180s"
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Equal(t, []string{"research", "interactive"}, cfg.Agent.SubAgent.AllowedCapabilities)
	require.Equal(t, "180s", cfg.Agent.SubAgent.Timeout)
}

// TestLoad_MemoryEmbeddingConfig 记忆向量化配置解析(12-记忆系统技术方案 §5.3):
// provider/base_url/api_key/model/dims/timeout 正确映射;provider 空 = 功能关闭。
func TestLoad_MemoryEmbeddingConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
memory:
  embedding:
    provider: "openai_compatible"
    base_url: "https://qianfan.baidubce.com/v2"
    api_key: "sk-embed"
    model: "bge-large-zh"
    dims: 1024
    timeout: "10s"
`
	require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	emb := cfg.Memory.Embedding
	require.Equal(t, "openai_compatible", emb.Provider)
	require.Equal(t, "https://qianfan.baidubce.com/v2", emb.BaseURL)
	require.Equal(t, "sk-embed", emb.APIKey)
	require.Equal(t, "bge-large-zh", emb.Model)
	require.Equal(t, 1024, emb.Dims)
	require.Equal(t, "10s", emb.Timeout)
}

// TestLoad_MemoryEmbeddingAbsent 旧配置无 memory.embedding 段:不报错,Provider 空(功能关闭,降级子串)。
func TestLoad_MemoryEmbeddingAbsent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("app:\n  name: \"omnibot\"\n  port: 8080\n"), 0o644))

	cfg, err := Load(path)
	require.NoError(t, err)
	require.Empty(t, cfg.Memory.Embedding.Provider)
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
