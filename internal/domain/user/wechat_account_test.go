package user

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewWechatAccount(t *testing.T) {
	user := NewUser()
	account := NewWechatAccount(user.ID, "test_openid_123")

	assert.NotNil(t, account)
	assert.Equal(t, user.ID, account.UserID)
	assert.Equal(t, "test_openid_123", account.OpenID)
	assert.Nil(t, account.UnionID)
	assert.WithinDuration(t, time.Now(), account.CreatedAt, time.Second)
}

func TestWechatAccount_SetUnionID(t *testing.T) {
	user := NewUser()
	account := NewWechatAccount(user.ID, "test_openid_123")

	unionID := "test_unionid_456"
	account.SetUnionID(unionID)

	assert.NotNil(t, account.UnionID)
	assert.Equal(t, unionID, *account.UnionID)
}
