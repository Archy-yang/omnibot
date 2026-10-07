package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"sync"
)

// LegacyDefaultKey 历史硬编码默认密钥——§17 之前的静默回退值。
// 仅允许两处使用:① 存量密文迁移(internal/db)解密旧数据;
// ② development 环境未配置密钥时的兜底(显眼告警)。任何路径不得用它加密新数据。
var LegacyDefaultKey = []byte("0123456789abcdef0123456789abcdef")

// ErrKeyNotConfigured 未注入全局密钥时走无参 Encrypt/Decrypt 的错误。
// 装配点必须保证启动时 Init;出现此错误属装配缺陷,不是运行时降级理由。
var ErrKeyNotConfigured = errors.New("crypto: global encrypt key not configured (must Init at startup)")

var (
	mu        sync.RWMutex
	globalKey []byte
)

// DeriveKey 任意长度密钥材料 → 32 字节 AES-256 密钥(sha256 派生)。
// 统一派生消除"材料长度不对要等到调用才报错"的坑。
func DeriveKey(material string) []byte {
	sum := sha256.Sum256([]byte(material))
	return sum[:]
}

// Init 注入全局加密密钥(密钥材料形式,内部 sha256 派生)。装配点启动时调用一次。
func Init(material string) {
	InitWithKey(DeriveKey(material))
}

// InitWithKey 注入全局加密密钥(原始 32 字节)。供 legacy 兜底与迁移路径使用。
func InitWithKey(key []byte) {
	mu.Lock()
	defer mu.Unlock()
	globalKey = key
}

// Configured 全局密钥是否已注入。
func Configured() bool {
	mu.RLock()
	defer mu.RUnlock()
	return globalKey != nil
}

func globalEncryptKey() ([]byte, error) {
	mu.RLock()
	defer mu.RUnlock()
	if globalKey == nil {
		return nil, ErrKeyNotConfigured
	}
	return globalKey, nil
}

// Encrypt AES-256-GCM 加密。
// 密钥解析:显式 key 参数优先(测试/迁移用);否则走启动时注入的全局密钥,
// 未注入返回 ErrKeyNotConfigured——不再静默回退默认密钥(§17)。
func Encrypt(plaintext string, key ...[]byte) (string, error) {
	var k []byte
	if len(key) > 0 {
		k = key[0]
	} else {
		var err error
		if k, err = globalEncryptKey(); err != nil {
			return "", err
		}
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

// Decrypt AES-256-GCM 解密。密钥解析规则同 Encrypt。
func Decrypt(ciphertext string, key ...[]byte) (string, error) {
	var k []byte
	if len(key) > 0 {
		k = key[0]
	} else {
		var err error
		if k, err = globalEncryptKey(); err != nil {
			return "", err
		}
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
