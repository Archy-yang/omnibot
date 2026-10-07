package api

// security.go — §17 密钥安全装配(2026-10):
// 加密密钥/JWT secret 的解析与 production fail-fast。
// 原则:配置不安全时系统拒绝启动,而不是静默用默认密钥继续工作。

import (
	"fmt"
	"os"

	"omnibot/pkg/config"
)

// envEncryptKey 旧版加密密钥环境变量(§17 前的唯一来源,保留为覆盖手段)。
const envEncryptKey = "LLM_CONFIG_ENCRYPT_KEY"

// minJWTSecretLength JWT secret 最低长度(32 字符起,防弱密钥)。
const minJWTSecretLength = 32

// resolveEncryptKeyMaterial 解析加密密钥材料:config.yaml security.encrypt_key 优先,
// 回落旧环境变量 LLM_CONFIG_ENCRYPT_KEY;都未配置返回空串。
func resolveEncryptKeyMaterial(cfg *config.Config) string {
	if cfg.Security.EncryptKey != "" {
		return cfg.Security.EncryptKey
	}
	return os.Getenv(envEncryptKey)
}

// validateSecurityForProduction production 环境的密钥配置校验(§17 fail-fast):
// 加密密钥、JWT secret 缺失或弱(过短)均返回错误,装配点以 Fatal 退出。
// 非 production 环境跳过(开发兜底行为由装配点另行处理并告警)。
func validateSecurityForProduction(cfg *config.Config) error {
	if cfg.App.Env != "production" {
		return nil
	}
	if resolveEncryptKeyMaterial(cfg) == "" {
		return fmt.Errorf("security: production 环境必须配置 security.encrypt_key(或环境变量 %s),拒绝启动(§17)", envEncryptKey)
	}
	if cfg.Auth.JWTSecret == "" {
		return fmt.Errorf("security: production 环境必须配置 auth.jwt_secret,拒绝启动(§17)")
	}
	if len(cfg.Auth.JWTSecret) < minJWTSecretLength {
		return fmt.Errorf("security: production 环境 auth.jwt_secret 长度不得少于 %d 字符,拒绝启动(§17)", minJWTSecretLength)
	}
	return nil
}
