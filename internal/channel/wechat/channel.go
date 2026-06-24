package wechat

import (
	"fmt"
	"time"

	domainchannel "omnibot/internal/domain/channel"
)

// Make sure Channel implements MessageChannel
var _ domainchannel.MessageChannel = (*Channel)(nil)

// Channel 微信消息通道实现。
//
// v1.9 起 Channel 纯无状态:入站 XML 解析由 Parse 函数提供,出站 XML 序列化由
// BuildResponseXML 提供,业务层(internal/api/wechat handler)只处理纯文本,不感知 XML。
//
// 注意:微信是同步通道,必须在 HTTP 响应里直接返回 XML——所以 SendText/SendReply
// 是 no-op,实际响应由 HTTP 层用 BuildResponseXML 包装后写出。
type Channel struct{}

// NewChannel 创建微信通道。v1.9 起无构造参数(响应里需要的公众号微信号从入站请求自带)。
func NewChannel() *Channel {
	return &Channel{}
}

// ChannelType 返回通道类型。
func (c *Channel) ChannelType() string {
	return "wechat"
}

// SendText 发送纯文本消息(no-op,微信同步通道由 HTTP 响应承载)。
func (c *Channel) SendText(channelUserID string, content string) error {
	return nil
}

// SendReply 回复特定消息。微信被动回复无引用语义,等价于 SendText。
func (c *Channel) SendReply(channelMessageID string, channelUserID string, content string) error {
	return c.SendText(channelUserID, content)
}

// IsAsync 微信不支持异步发送(必须在 5 秒内响应)。
func (c *Channel) IsAsync() bool {
	return false
}

// BuildResponseXML 构建微信被动回复 XML。
//
//   - toOpenID  : 接收者(用户的 OpenID)→ XML 的 <ToUserName>
//   - fromGhID  : 发送者(公众号微信号,从入站请求的 ToUserName 拿)→ XML 的 <FromUserName>
//   - content   : 文本内容
//
// v1.9 改为纯函数(无 channel 状态依赖):入站请求自带公众号微信号,从那里取即可,
// 避免 channel 持状态又跨公众号场景下需要 SetToUserName 切换。
func (c *Channel) BuildResponseXML(toOpenID, fromGhID, content string) string {
	now := time.Now().Unix()
	return fmt.Sprintf(`<xml>
	<ToUserName><![CDATA[%s]]></ToUserName>
	<FromUserName><![CDATA[%s]]></FromUserName>
	<CreateTime>%d</CreateTime>
	<MsgType><![CDATA[text]]></MsgType>
	<Content><![CDATA[%s]]></Content>
	</xml>`, toOpenID, fromGhID, now, content)
}
