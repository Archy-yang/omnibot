package crypto

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAES_EncryptDecrypt(t *testing.T) {
	// 测试密钥
	key := []byte("01234567890123456789012345678901") // 32 bytes for AES-256
	plaintext := "sk-this-is-a-test-api-key-123456"

	encrypted, err := Encrypt(plaintext, key)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)
	assert.NotEqual(t, plaintext, encrypted)

	// 验证可以解密还原
	decrypted, err := Decrypt(encrypted, key)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestAES_DifferentNonce(t *testing.T) {
	key := []byte("01234567890123456789012345678901")
	plaintext := "sk-test-key"

	enc1, _ := Encrypt(plaintext, key)
	enc2, _ := Encrypt(plaintext, key)

	// 相同明文，不同 nonce 应该产生不同密文
	assert.NotEqual(t, enc1, enc2)
}

func TestAES_WrongKey(t *testing.T) {
	key1 := []byte("01234567890123456789012345678901")
	key2 := []byte("11111111111111111111111111111111")
	plaintext := "sk-test-key"

	encrypted, _ := Encrypt(plaintext, key1)
	decrypted, err := Decrypt(encrypted, key2)

	assert.Error(t, err)
	assert.NotEqual(t, plaintext, decrypted)
}

func TestAES_InvalidKeyLength(t *testing.T) {
	badKey := []byte("short") // 5 bytes, not valid for AES
	_, err := Encrypt("test", badKey)
	assert.Error(t, err)
}

// ---- §17 密钥治理:显式注入 + fail-fast(2026-10) ----

// resetGlobalKey 恢复"未注入"状态,隔离全局状态对测试的影响。
func resetGlobalKey(t *testing.T) {
	t.Helper()
	mu.Lock()
	globalKey = nil
	mu.Unlock()
}

func TestDeriveKey_Deterministic32Bytes(t *testing.T) {
	k1 := DeriveKey("my-secret-material")
	k2 := DeriveKey("my-secret-material")
	k3 := DeriveKey("other-material")

	assert.Len(t, k1, 32) // AES-256 要求 32 字节
	assert.Equal(t, k1, k2)
	assert.NotEqual(t, k1, k3)
}

func TestInit_MaterialEnablesGlobalKeyPath(t *testing.T) {
	resetGlobalKey(t)
	defer resetGlobalKey(t)

	assert.False(t, Configured(), "初始不应已配置")

	Init("my-secret-material")
	assert.True(t, Configured())

	enc, err := Encrypt("sk-test")
	require.NoError(t, err)
	dec, err := Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, "sk-test", dec)

	// 显式 key 参数路径不受全局状态影响
	other := []byte("11111111111111111111111111111111")
	enc2, err := Encrypt("sk-test", other)
	require.NoError(t, err)
	_, err = Decrypt(enc2) // 全局 key 解不开显式 key 的密文
	assert.Error(t, err)
}

func TestInitWithKey_RawKeyPath(t *testing.T) {
	resetGlobalKey(t)
	defer resetGlobalKey(t)

	InitWithKey([]byte("01234567890123456789012345678901"))
	enc, err := Encrypt("sk-test")
	require.NoError(t, err)
	dec, err := Decrypt(enc)
	require.NoError(t, err)
	assert.Equal(t, "sk-test", dec)
}

func TestNotConfigured_GlobalPathFails(t *testing.T) {
	resetGlobalKey(t)

	_, err := Encrypt("sk-test")
	assert.ErrorIs(t, err, ErrKeyNotConfigured)
	_, err = Decrypt("c2FsdA==")
	assert.ErrorIs(t, err, ErrKeyNotConfigured)
}
