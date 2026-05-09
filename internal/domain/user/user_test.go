package user

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewUser(t *testing.T) {
	user := NewUser()

	assert.NotNil(t, user)
	assert.Equal(t, StatusNormal, user.Status)
	assert.False(t, user.PhoneVerified)
	assert.Empty(t, user.Phone)
	assert.WithinDuration(t, time.Now(), user.CreatedAt, time.Second)
	assert.WithinDuration(t, time.Now(), user.UpdatedAt, time.Second)
}

func TestUser_BindPhone(t *testing.T) {
	user := NewUser()

	user.BindPhone("13800138000")

	assert.Equal(t, "13800138000", *user.Phone)
	assert.True(t, user.PhoneVerified)
	assert.NotNil(t, user.PhoneBindTime)
}

func TestUser_StatusFlow(t *testing.T) {
	user := NewUser()

	user.Ban()
	assert.Equal(t, StatusBanned, user.Status)

	user.Unban()
	assert.Equal(t, StatusNormal, user.Status)

	user.SoftDelete()
	assert.Equal(t, StatusDeleted, user.Status)
}
