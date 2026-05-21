package channel

// MessageChannel 消息通道接口
// 所有对话入口（微信、飞书、Web 等）都实现这个接口
type MessageChannel interface {
	// ChannelType 返回通道类型标识
	ChannelType() string

	// SendText 发送纯文本消息给用户
	SendText(channelUserID string, content string) error

	// SendReply 回复特定消息（有些通道支持引用回复）
	SendReply(channelMessageID string, channelUserID string, content string) error

	// IsAsync 返回这个通道是否支持异步发送
	// true = 可以后台发送，不需要在 HTTP 请求里直接返回
	// false = 必须在 HTTP 请求里同步返回响应内容
	IsAsync() bool
}
