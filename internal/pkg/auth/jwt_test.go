package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "test-secret-32-chars-min-len-ok!"

func TestGenerateAndParse_RoundTrip(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	token, err := svc.GenerateToken(42)
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	userID, err := svc.ParseToken(token)
	require.NoError(t, err)
	assert.Equal(t, int64(42), userID)
}

func TestParse_Expired(t *testing.T) {
	// TTL 为负,签出来的 token 已过期
	svc := NewJWTService(testSecret, -time.Hour)

	token, err := svc.GenerateToken(1)
	require.NoError(t, err)

	_, err = svc.ParseToken(token)
	assert.Error(t, err)
}

func TestParse_Tampered(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	token, err := svc.GenerateToken(1)
	require.NoError(t, err)

	// 篡改最后一个字符
	tampered := token[:len(token)-1] + string(alterLast(token[len(token)-1]))
	_, err = svc.ParseToken(tampered)
	assert.Error(t, err)
}

func TestParse_WrongSecret(t *testing.T) {
	signer := NewJWTService(testSecret, time.Hour)
	verifier := NewJWTService("different-secret-32-chars-yes-ok", time.Hour)

	token, err := signer.GenerateToken(1)
	require.NoError(t, err)

	_, err = verifier.ParseToken(token)
	assert.Error(t, err)
}

func TestParse_Malformed(t *testing.T) {
	svc := NewJWTService(testSecret, time.Hour)

	cases := []string{"", "not-a-token", "aaa.bbb.ccc"}
	for _, c := range cases {
		_, err := svc.ParseToken(c)
		assert.Error(t, err, "expected error for %q", c)
	}
}

// alterLast 把一个字符换成另一个字符(用于篡改测试)
func alterLast(c byte) byte {
	if c == 'A' {
		return 'B'
	}
	return 'A'
}
