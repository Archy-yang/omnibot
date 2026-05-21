package channel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	domainchannel "omnibot/internal/domain/channel"
)

// Make sure MockChannel implements MessageChannel
var _ domainchannel.MessageChannel = (*MockChannel)(nil)

// MockChannel 测试用的通道实现
type MockChannel struct {
	channelType string
	isAsync     bool
	sentTexts   []struct {
		userID  string
		content string
	}
	sentReplies []struct {
		msgID   string
		userID  string
		content string
	}
}

func NewMockChannel(channelType string, isAsync bool) *MockChannel {
	return &MockChannel{
		channelType: channelType,
		isAsync:     isAsync,
	}
}

func (m *MockChannel) ChannelType() string {
	return m.channelType
}

func (m *MockChannel) SendText(channelUserID string, content string) error {
	m.sentTexts = append(m.sentTexts, struct {
		userID  string
		content string
	}{channelUserID, content})
	return nil
}

func (m *MockChannel) SendReply(channelMessageID string, channelUserID string, content string) error {
	m.sentReplies = append(m.sentReplies, struct {
		msgID   string
		userID  string
		content string
	}{channelMessageID, channelUserID, content})
	return nil
}

func (m *MockChannel) IsAsync() bool {
	return m.isAsync
}

func (m *MockChannel) Reset() {
	m.sentTexts = nil
	m.sentReplies = nil
}

func TestFactory_RegisterAndGet(t *testing.T) {
	// 清空已注册的通道
	oldChannels := channels
	channels = make(map[string]domainchannel.MessageChannel)
	defer func() { channels = oldChannels }()

	// 注册 mock 通道
	mock := NewMockChannel("test-channel", false)
	Register(mock)

	// 获取通道
	ch, ok := Get("test-channel")
	assert.True(t, ok)
	assert.NotNil(t, ch)
	assert.Equal(t, "test-channel", ch.ChannelType())

	// 获取不存在的通道
	ch, ok = Get("non-existent")
	assert.False(t, ok)
	assert.Nil(t, ch)
}

func TestFactory_List(t *testing.T) {
	oldChannels := channels
	channels = make(map[string]domainchannel.MessageChannel)
	defer func() { channels = oldChannels }()

	// 注册多个通道
	Register(NewMockChannel("channel1", false))
	Register(NewMockChannel("channel2", true))

	types := List()
	assert.Len(t, types, 2)
	assert.Contains(t, types, "channel1")
	assert.Contains(t, types, "channel2")
}

func TestMockChannel_SendText(t *testing.T) {
	mock := NewMockChannel("test", false)
	mock.Reset()

	err := mock.SendText("user123", "hello world")
	assert.NoError(t, err)
	assert.Len(t, mock.sentTexts, 1)
	assert.Equal(t, "user123", mock.sentTexts[0].userID)
	assert.Equal(t, "hello world", mock.sentTexts[0].content)
}

func TestMockChannel_SendReply(t *testing.T) {
	mock := NewMockChannel("test", false)
	mock.Reset()

	err := mock.SendReply("msg456", "user123", "reply content")
	assert.NoError(t, err)
	assert.Len(t, mock.sentReplies, 1)
	assert.Equal(t, "msg456", mock.sentReplies[0].msgID)
	assert.Equal(t, "user123", mock.sentReplies[0].userID)
	assert.Equal(t, "reply content", mock.sentReplies[0].content)
}

func TestMockChannel_IsAsync(t *testing.T) {
	syncChannel := NewMockChannel("sync", false)
	asyncChannel := NewMockChannel("async", true)

	assert.False(t, syncChannel.IsAsync())
	assert.True(t, asyncChannel.IsAsync())
}
