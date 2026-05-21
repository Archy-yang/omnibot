package wechat

import (
	"fmt"
	"time"

	domainchannel "omnibot/internal/domain/channel"
)

// Make sure Channel implements MessageChannel
var _ domainchannel.MessageChannel = (*Channel)(nil)

// Channel 微信消息通道实现
// 注意：微信是同步通道，必须在 HTTP 请求的响应里直接返回 XML
// 所以 SendText/SendReply 不是真的发送网络请求，而是返回要响应的 XML 内容
type Channel struct {
	toUserName string // 公众号的微信号（消息接收者）
}

// NewChannel 创建微信通道
// toUserName 是公众号的微信号（在接收消息时的 ToUserName 字段）
func NewChannel(toUserName string) *Channel {
	return &Channel{
		toUserName: toUserName,
	}
}

// ChannelType 返回通道类型
func (c *Channel) ChannelType() string {
	return "wechat"
}

// SendText 发送纯文本消息
// 对于微信，返回的是要在 HTTP 响应里返回的 XML 字符串
func (c *Channel) SendText(channelUserID string, content string) error {
	// 微信通道是同步的，SendText 在这里只是构建 XML
	// 实际发送由 HTTP 响应处理完成
	return nil
}

// SendReply 回复特定消息
// 微信被动回复没有引用功能，所以和 SendText 行为一致
func (c *Channel) SendReply(channelMessageID string, channelUserID string, content string) error {
	return c.SendText(channelUserID, content)
}

// IsAsync 返回通道是否支持异步发送
// 微信不支持异步发送（必须在 5 秒内响应）
func (c *Channel) IsAsync() bool {
	return false
}

// BuildResponseXML 构建微信响应 XML
// 这个是微信特有的方法，因为微信需要同步返回 XML
func (c *Channel) BuildResponseXML(fromUserName string, content string) string {
	now := time.Now().Unix()
	response := fmt.Sprintf(`<xml>
	<ToUserName><![CDATA[%s]]></ToUserName>
	<FromUserName><![CDATA[%s]]></FromUserName>
	<CreateTime>%d</CreateTime>
	<MsgType><![CDATA[text]]></MsgType>
	<Content><![CDATA[%s]]></Content>
	</xml>`, fromUserName, c.toUserName, now, content)
	return response
}

// SetToUserName 更新接收者（公众号微信号）
// 用于不同公众号场景
func (c *Channel) SetToUserName(toUserName string) {
	c.toUserName = toUserName
}
