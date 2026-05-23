package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMemory(t *testing.T) {
	before := time.Now()

	m := NewMemory(123, "我偏好简洁直接的回答")

	assert.Equal(t, int64(123), m.UserID)
	assert.Equal(t, "我偏好简洁直接的回答", m.Content)
	assert.False(t, m.CreatedAt.Before(before))
	assert.False(t, m.UpdatedAt.Before(before))
}

func TestMemory_TableName(t *testing.T) {
	assert.Equal(t, "memories", Memory{}.TableName())
}
