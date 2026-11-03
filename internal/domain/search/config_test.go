package search

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// N1 domain 层:配置实体校验与默认值。测试先行(32 号 §7)。

func TestConfig_Normalize_FillsDefaults(t *testing.T) {
	cfg := &Config{}

	require.NoError(t, cfg.Normalize())

	assert.Equal(t, ProviderDeepSeek, cfg.Provider)
	assert.Equal(t, DefaultBaseURL, cfg.BaseURL)
	assert.Equal(t, DefaultCount, cfg.DefaultCount)
	assert.Equal(t, DefaultTimeoutSeconds, cfg.TimeoutSeconds)
}

func TestConfig_Normalize_KeepsUserValues(t *testing.T) {
	cfg := &Config{
		Provider:       "deepseek",
		BaseURL:        "https://proxy.example.com/",
		DefaultCount:   8,
		TimeoutSeconds: 30,
	}

	require.NoError(t, cfg.Normalize())

	assert.Equal(t, "https://proxy.example.com/", cfg.BaseURL)
	assert.Equal(t, 8, cfg.DefaultCount)
	assert.Equal(t, 30, cfg.TimeoutSeconds)
}

func TestConfig_Normalize_RejectsInvalidProvider(t *testing.T) {
	cfg := &Config{Provider: "tavily"}

	err := cfg.Normalize()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "服务商")
}

func TestConfig_Normalize_RejectsBadScheme(t *testing.T) {
	for _, raw := range []string{
		"ftp://api.deepseek.com",
		"file:///etc/passwd",
		"api.deepseek.com", // 缺 scheme
		"https://",         // 缺主机名
	} {
		cfg := &Config{BaseURL: raw}
		err := cfg.Normalize()
		require.Error(t, err, "应拒绝:%s", raw)
		assert.Contains(t, err.Error(), "服务地址")
	}
}

// 内网/本机地址必须允许:这是用户自己填的服务地址(用户信任域),
// 与 web_read 的 SSRF 防护场景不同——不是 LLM 可控的 URL。
func TestConfig_Normalize_AllowsPrivateBaseURL(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:8080",
		"http://192.168.1.10/search",
	} {
		cfg := &Config{BaseURL: raw}
		require.NoError(t, cfg.Normalize(), "应允许:%s", raw)
	}
}

func TestConfig_Normalize_RejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"count too large", Config{DefaultCount: 21}, "条数"},
		{"count negative", Config{DefaultCount: -1}, "条数"},
		{"timeout too small", Config{TimeoutSeconds: 4}, "超时"},
		{"timeout too large", Config{TimeoutSeconds: 61}, "超时"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := tc.cfg
			err := cfg.Normalize()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestConfig_IsUsable(t *testing.T) {
	base := Config{
		Provider: ProviderDeepSeek,
		BaseURL:  DefaultBaseURL,
		APIKey:   "ciphertext",
		Enabled:  true,
	}
	assert.True(t, base.IsUsable())

	disabled := base
	disabled.Enabled = false
	assert.False(t, disabled.IsUsable())

	noKey := base
	noKey.APIKey = ""
	assert.False(t, noKey.IsUsable())

	noURL := base
	noURL.BaseURL = ""
	assert.False(t, noURL.IsUsable())
}

func TestMaskKey(t *testing.T) {
	assert.Equal(t, "", MaskKey(""))
	assert.Equal(t, "***", MaskKey("short")) // 太短不做部分暴露
	assert.Equal(t, "sk-ab...89", MaskKey("sk-abcdefghijklmnopqrstuvwxyz89"))
}

func TestConfig_CountOr(t *testing.T) {
	cfg := &Config{DefaultCount: 0}
	assert.Equal(t, DefaultCount, cfg.CountOr(0))
	cfg.DefaultCount = 7
	assert.Equal(t, 7, cfg.CountOr(0))
	assert.Equal(t, 3, cfg.CountOr(3)) // 调用方显式指定优先
	assert.Equal(t, 7, cfg.CountOr(999)) // 越界回落到配置值
}
