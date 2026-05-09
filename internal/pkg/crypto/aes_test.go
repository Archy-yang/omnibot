package crypto

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
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
