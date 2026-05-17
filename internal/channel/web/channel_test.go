package web

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWebChannel_ChannelType(t *testing.T) {
	ch := NewChannel()
	assert.Equal(t, "web", ch.ChannelType())
}

func TestWebChannel_IsAsync(t *testing.T) {
	ch := NewChannel()
	assert.True(t, ch.IsAsync(), "Web channel should be async capable")
}

func TestWebChannel_SendText(t *testing.T) {
	ch := NewChannel()
	// Web channel SendText is a no-op for now (async capability for future)
	err := ch.SendText("user123", "test message")
	assert.NoError(t, err)
}

func TestWebChannel_SendReply(t *testing.T) {
	ch := NewChannel()
	err := ch.SendReply("msg456", "user123", "reply content")
	assert.NoError(t, err)
}
