package channel

// MessageChannel 消息渠道接口
type MessageChannel interface {
	// ChannelType 返回渠道类型
	ChannelType() string
	// IsAsync 是否异步发送
	IsAsync() bool
	// SendText 发送文本消息
	SendText(channelUserID string, content string) error
	// SendReply 回复特定消息
	SendReply(channelMessageID string, channelUserID string, content string) error
}
