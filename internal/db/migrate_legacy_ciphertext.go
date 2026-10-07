package db

import (
	"fmt"
	"strings"

	"omnibot/internal/domain/mcp"
	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// 注:调用方须保证 newKey 与装配点 crypto.Init 的密钥一致(启动顺序:Init → 迁移 → 服务可用)。

// MigrateLegacyCiphertext §17 存量密文迁移(2026-10,启动自愈,幂等):
// 把用 legacy 默认密钥(internal/pkg/crypto.LegacyDefaultKey)加密的存量密文,
// 重加密为当前生效密钥 newKey。装配点在 crypto.Init(新密钥) 之后、服务可用之前调用。
//
// 逐字段策略:
//   - user_llm_configs.api_key / embedding_api_key:裸密文(无前缀)。
//     先试 newKey(已是新密文,跳过),失败再试 legacy(命中则重加密),都失败=损坏,告警跳过。
//   - mcp_servers.api_key / o_auth_client_secret / o_auth_tokens:带 enc: 前缀;
//     无前缀视为历史明文(mcp_manager.decryptSecret 同语义),跳过不迁移。
//
// 返回迁移(重加密)的字段数。损坏行不阻断启动——按需手工处理。
func MigrateLegacyCiphertext(gdb *gorm.DB, newKey []byte) (int, error) {
	migrated := 0

	// ---- user_llm_configs:裸密文字段 ----
	var configs []user.LLMConfig
	if err := gdb.Find(&configs).Error; err != nil {
		return migrated, fmt.Errorf("db: 密文迁移读 user_llm_configs: %w", err)
	}
	for _, c := range configs {
		for field, stored := range map[string]string{
			"api_key":           c.APIKey,
			"embedding_api_key": c.EmbeddingAPIKey,
		} {
			if stored == "" {
				continue
			}
			if _, err := crypto.Decrypt(stored, newKey); err == nil {
				continue // 已是新密文
			}
			plain, err := crypto.Decrypt(stored, crypto.LegacyDefaultKey)
			if err != nil {
				logger.WarnWithFields("密文迁移:字段无法解密(新/旧密钥均失败),跳过",
					zap.Int64("config_id", c.ID), zap.String("field", field))
				continue
			}
			reEnc, err := crypto.Encrypt(plain, newKey)
			if err != nil {
				return migrated, fmt.Errorf("db: 密文迁移重加密 config %d %s: %w", c.ID, field, err)
			}
			if err := gdb.Model(&user.LLMConfig{}).Where("id = ?", c.ID).
				Update(field, reEnc).Error; err != nil {
				return migrated, fmt.Errorf("db: 密文迁移写回 config %d %s: %w", c.ID, field, err)
			}
			migrated++
		}
	}

	// ---- mcp_servers:enc: 前缀字段 ----
	var servers []mcp.MCPServer
	if err := gdb.Find(&servers).Error; err != nil {
		return migrated, fmt.Errorf("db: 密文迁移读 mcp_servers: %w", err)
	}
	for _, s := range servers {
		for field, stored := range map[string]string{
			"api_key":              s.APIKey,
			"o_auth_client_secret": s.OAuthClientSecret,
			"o_auth_tokens":        s.OAuthTokens,
		} {
			if stored == "" || !strings.HasPrefix(stored, "enc:") {
				continue // 空或历史明文
			}
			body := strings.TrimPrefix(stored, "enc:")
			if _, err := crypto.Decrypt(body, newKey); err == nil {
				continue // 已是新密文
			}
			plain, err := crypto.Decrypt(body, crypto.LegacyDefaultKey)
			if err != nil {
				logger.WarnWithFields("密文迁移:字段无法解密(新/旧密钥均失败),跳过",
					zap.Int64("server_id", s.ID), zap.String("field", field))
				continue
			}
			reEnc, err := crypto.Encrypt(plain, newKey)
			if err != nil {
				return migrated, fmt.Errorf("db: 密文迁移重加密 server %d %s: %w", s.ID, field, err)
			}
			if err := gdb.Model(&mcp.MCPServer{}).Where("id = ?", s.ID).
				Update(field, "enc:"+reEnc).Error; err != nil {
				return migrated, fmt.Errorf("db: 密文迁移写回 server %d %s: %w", s.ID, field, err)
			}
			migrated++
		}
	}

	return migrated, nil
}
