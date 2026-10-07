package api

import (
	"strings"
	"testing"

	"omnibot/pkg/config"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// §17 密钥安全装配测试(2026-10):密钥材料解析优先级 + production fail-fast。

func TestResolveEncryptKeyMaterial_ConfigPriority(t *testing.T) {
	t.Setenv("LLM_CONFIG_ENCRYPT_KEY", "from-env")
	cfg := &config.Config{}
	cfg.Security.EncryptKey = "from-config"

	assert.Equal(t, "from-config", resolveEncryptKeyMaterial(cfg), "config.yaml 优先于旧环境变量")
}

func TestResolveEncryptKeyMaterial_EnvFallback(t *testing.T) {
	t.Setenv("LLM_CONFIG_ENCRYPT_KEY", "from-env")
	cfg := &config.Config{}

	assert.Equal(t, "from-env", resolveEncryptKeyMaterial(cfg))
}

func TestResolveEncryptKeyMaterial_Empty(t *testing.T) {
	t.Setenv("LLM_CONFIG_ENCRYPT_KEY", "")
	cfg := &config.Config{}

	assert.Equal(t, "", resolveEncryptKeyMaterial(cfg))
}

func productionCfg() *config.Config {
	cfg := &config.Config{}
	cfg.App.Env = "production"
	cfg.Security.EncryptKey = "a-secure-random-material"
	cfg.Auth.JWTSecret = strings.Repeat("x", 40)
	return cfg
}

func TestValidateSecurityForProduction_OK(t *testing.T) {
	assert.NoError(t, validateSecurityForProduction(productionCfg()))
}

func TestValidateSecurityForProduction_MissingEncryptKey(t *testing.T) {
	cfg := productionCfg()
	cfg.Security.EncryptKey = ""
	err := validateSecurityForProduction(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "encrypt_key")
}

func TestValidateSecurityForProduction_MissingJWTSecret(t *testing.T) {
	cfg := productionCfg()
	cfg.Auth.JWTSecret = ""
	err := validateSecurityForProduction(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwt_secret")
}

func TestValidateSecurityForProduction_JWTSecretTooShort(t *testing.T) {
	cfg := productionCfg()
	cfg.Auth.JWTSecret = "short"
	err := validateSecurityForProduction(cfg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "jwt_secret")
}

func TestValidateSecurityForDevelopment_Skipped(t *testing.T) {
	cfg := productionCfg()
	cfg.App.Env = "development"
	cfg.Security.EncryptKey = ""
	cfg.Auth.JWTSecret = ""
	assert.NoError(t, validateSecurityForProduction(cfg), "非 production 环境不做 fail-fast(装配点另行告警)")
}
