package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestLoad_SilenceGapConfig M8.1 沉淀切分的 silence_gap 解析(架构复评 P3-12):
// "10m" 字符串须经 mapstructure decode hook 正确解析为 time.Duration;
// 缺省时为 0(装配点回落默认 10m);非法值不应让 Load 失败(解码宽松语义)。
func TestLoad_SilenceGapConfig(t *testing.T) {
	t.Run("字符串时长解析", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		yaml := `
memory:
  extraction:
    enabled: true
    batch_size: 40
    silence_gap: "10m"
`
		require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

		cfg, err := Load(path)
		require.NoError(t, err)
		require.True(t, cfg.Memory.Extraction.Enabled)
		require.Equal(t, 40, cfg.Memory.Extraction.BatchSize)
		require.Equal(t, 10*time.Minute, cfg.Memory.Extraction.SilenceGap)
	})

	t.Run("缺省为零值由装配点兜底", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		require.NoError(t, os.WriteFile(path, []byte("app:\n  name: x\n"), 0o644))

		cfg, err := Load(path)
		require.NoError(t, err)
		require.Equal(t, time.Duration(0), cfg.Memory.Extraction.SilenceGap)
	})

	t.Run("非法值拒绝启动(fail-fast)", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "config.yaml")
		yaml := `
memory:
  extraction:
    silence_gap: "600"
`
		require.NoError(t, os.WriteFile(path, []byte(yaml), 0o644))

		// 实测(比复评报告预想更好):decode hook 对非法时长直接报错,
		// Load 返回 error → 服务拒绝启动,而非静默落到零值(§17 同款原则)
		_, err := Load(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "silence_gap")
	})
}
