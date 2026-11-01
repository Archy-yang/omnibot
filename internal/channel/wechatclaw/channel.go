package wechatclaw

import (
	"context"
	"errors"

	domainchannel "omnibot/internal/domain/channel"
)

// ChannelType 通道类型标识(写入 user_channels.channel_type)。
const ChannelType = "wechat_claw"

// ErrNotConnected 当前没有可用的 ClawBot 会话(未登录 / 已断开)。
var ErrNotConnected = errors.New("wechatclaw: channel not connected")

// Channel 微信 ClawBot 通道(异步发送)。
//
// 与公众号 wechat 不同:ClawBot 不是 HTTP 回调被动回复,而是长轮询拿消息、
// 独立 HTTP 请求发消息,所以 SendText 走 Sender,且需要当前登录态。
// sender 用 provider 动态取:重新扫码会替换底层 client,通道实例保持不变。
type Channel struct {
	provider func() *Sender
}

// NewChannel 创建通道。provider 返回 nil 表示当前未连接。
func NewChannel(provider func() *Sender) *Channel {
	return &Channel{provider: provider}
}

// ChannelType 返回通道类型。
func (c *Channel) ChannelType() string { return ChannelType }

// IsAsync ClawBot 支持异步/主动发送(长轮询,不受 HTTP 响应约束)。
func (c *Channel) IsAsync() bool { return true }

// SendText 发送文本消息。未连接时返回 ErrNotConnected(调用方降级为仅落库)。
func (c *Channel) SendText(channelUserID string, content string) error {
	sender := c.provider()
	if sender == nil {
		return ErrNotConnected
	}
	return sender.SendText(context.Background(), channelUserID, content)
}

// SendReply ClawBot 无引用回复语义,等价 SendText。
func (c *Channel) SendReply(channelMessageID string, channelUserID string, content string) error {
	return c.SendText(channelUserID, content)
}

var _ domainchannel.MessageChannel = (*Channel)(nil)
