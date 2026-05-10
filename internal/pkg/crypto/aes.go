package crypto

import (
    "crypto/aes"
    "crypto/cipher"
    "crypto/rand"
    "encoding/base64"
    "errors"
    "io"
    "os"
)

// 获取加密密钥，从环境变量 LLM_CONFIG_ENCRYPT_KEY
func getEncryptKey() []byte {
    key := os.Getenv("LLM_CONFIG_ENCRYPT_KEY")
    if key == "" {
        // 默认密钥，仅用于测试，生产环境必须配置（32字节）
        return []byte("0123456789abcdef0123456789abcdef")
    }
    return []byte(key)
}

// Encrypt AES-256-GCM 加密
func Encrypt(plaintext string, key ...[]byte) (string, error) {
    var k []byte
    if len(key) > 0 {
        k = key[0]
    } else {
        k = getEncryptKey()
    }

    block, err := aes.NewCipher(k)
    if err != nil {
        return "", err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return "", err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return "", err
    }

    ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
    return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt AES-256-GCM 解密
func Decrypt(ciphertext string, key ...[]byte) (string, error) {
    var k []byte
    if len(key) > 0 {
        k = key[0]
    } else {
        k = getEncryptKey()
    }

    data, err := base64.StdEncoding.DecodeString(ciphertext)
    if err != nil {
        return "", err
    }

    block, err := aes.NewCipher(k)
    if err != nil {
        return "", err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return "", err
    }

    nonceSize := gcm.NonceSize()
    if len(data) < nonceSize {
        return "", errors.New("ciphertext too short")
    }

    nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]
    plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
    if err != nil {
        return "", err
    }

    return string(plaintext), nil
}
