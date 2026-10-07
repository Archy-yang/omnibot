package db

import (
	"strings"
	"testing"

	"omnibot/internal/domain/mcp"
	"omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// §17 存量密文迁移测试(2026-10):legacy 默认密钥密文 → 新密钥重加密,
// 新密文不动,损坏密文跳过告警,幂等可重跑。

const newKeyMaterial = "migration-test-new-key"

var userOne int64 = 1

func setupCiphertextDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&user.LLMConfig{}, &mcp.MCPServer{}))
	return db
}

func legacyEncrypt(t *testing.T, plain string) string {
	t.Helper()
	enc, err := crypto.Encrypt(plain, crypto.LegacyDefaultKey)
	require.NoError(t, err)
	return enc
}

func newKeyEncrypt(t *testing.T, plain string) string {
	t.Helper()
	enc, err := crypto.Encrypt(plain, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	return enc
}

func TestMigrateLegacyCiphertext_LegacyRowsReEncrypted(t *testing.T) {
	db := setupCiphertextDB(t)
	cfg := &user.LLMConfig{UserID: 1, APIKey: legacyEncrypt(t, "sk-legacy-llm"), EmbeddingAPIKey: legacyEncrypt(t, "sk-legacy-emb"), Status: user.LLMConfigStatusNormal}
	require.NoError(t, db.Create(cfg).Error)
	srv := &mcp.MCPServer{UserID: &userOne, Name: "s1", BaseURL: "https://x", AuthType: "bearer", APIKey: "enc:" + legacyEncrypt(t, "mcp-plain")}
	require.NoError(t, db.Create(srv).Error)

	n, err := MigrateLegacyCiphertext(db, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	// 全部可用新密钥解密
	var gotCfg user.LLMConfig
	require.NoError(t, db.First(&gotCfg, cfg.ID).Error)
	plain, err := crypto.Decrypt(gotCfg.APIKey, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	assert.Equal(t, "sk-legacy-llm", plain)
	plain, err = crypto.Decrypt(gotCfg.EmbeddingAPIKey, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	assert.Equal(t, "sk-legacy-emb", plain)

	var gotSrv mcp.MCPServer
	require.NoError(t, db.First(&gotSrv, srv.ID).Error)
	assert.True(t, strings.HasPrefix(gotSrv.APIKey, "enc:"), "enc: 前缀必须保留")
	plain, err = crypto.Decrypt(strings.TrimPrefix(gotSrv.APIKey, "enc:"), crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	assert.Equal(t, "mcp-plain", plain)
}

func TestMigrateLegacyCiphertext_NewCiphertextUntouched(t *testing.T) {
	db := setupCiphertextDB(t)
	fresh := newKeyEncrypt(t, "sk-fresh")
	cfg := &user.LLMConfig{UserID: 1, APIKey: fresh, Status: user.LLMConfigStatusNormal}
	require.NoError(t, db.Create(cfg).Error)

	n, err := MigrateLegacyCiphertext(db, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	var got user.LLMConfig
	require.NoError(t, db.First(&got, cfg.ID).Error)
	assert.Equal(t, fresh, got.APIKey) // 密文逐字节不变(未重写)
}

func TestMigrateLegacyCiphertext_EmptyAndCorruptSkipped(t *testing.T) {
	db := setupCiphertextDB(t)
	cfg := &user.LLMConfig{UserID: 1, APIKey: "not-valid-ciphertext!!", EmbeddingAPIKey: "", Status: user.LLMConfigStatusNormal}
	require.NoError(t, db.Create(cfg).Error)
	// 无 enc: 前缀的 MCP 字段视为历史明文,跳过不迁移
	srv := &mcp.MCPServer{UserID: &userOne, Name: "s2", BaseURL: "https://x", AuthType: "bearer", APIKey: "raw-plaintext-key"}
	require.NoError(t, db.Create(srv).Error)

	n, err := MigrateLegacyCiphertext(db, crypto.DeriveKey(newKeyMaterial))
	require.NoError(t, err) // 损坏行跳过,不阻断启动
	assert.Equal(t, 0, n)

	var gotCfg user.LLMConfig
	require.NoError(t, db.First(&gotCfg, cfg.ID).Error)
	assert.Equal(t, "not-valid-ciphertext!!", gotCfg.APIKey)
	var gotSrv mcp.MCPServer
	require.NoError(t, db.First(&gotSrv, srv.ID).Error)
	assert.Equal(t, "raw-plaintext-key", gotSrv.APIKey)
}

func TestMigrateLegacyCiphertext_Idempotent(t *testing.T) {
	db := setupCiphertextDB(t)
	cfg := &user.LLMConfig{UserID: 1, APIKey: legacyEncrypt(t, "sk-idem"), Status: user.LLMConfigStatusNormal}
	require.NoError(t, db.Create(cfg).Error)

	key := crypto.DeriveKey(newKeyMaterial)
	n1, err := MigrateLegacyCiphertext(db, key)
	require.NoError(t, err)
	n2, err := MigrateLegacyCiphertext(db, key)
	require.NoError(t, err)
	assert.Equal(t, 1, n1)
	assert.Equal(t, 0, n2)
}
