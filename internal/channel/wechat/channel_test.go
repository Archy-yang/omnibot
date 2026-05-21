package wechat

import (
	"encoding/xml"
	"testing"

	"github.com/stretchr/testify/assert"
)

// WechatResponse XML 响应结构
type WechatResponse struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content"`
}

func TestChannel_ChannelType(t *testing.T) {
	ch := NewChannel("test-bot")
	assert.Equal(t, "wechat", ch.ChannelType())
}

func TestChannel_IsAsync(t *testing.T) {
	ch := NewChannel("test-bot")
	assert.False(t, ch.IsAsync(), "WeChat channel should be sync")
}

func TestChannel_SendText(t *testing.T) {
	ch := NewChannel("test-bot")
	err := ch.SendText("user123", "hello")
	assert.NoError(t, err)
}

func TestChannel_SendReply(t *testing.T) {
	ch := NewChannel("test-bot")
	err := ch.SendReply("msg456", "user123", "reply content")
	assert.NoError(t, err)
}

func TestChannel_BuildResponseXML(t *testing.T) {
	ch := NewChannel("bot-account")
	xmlStr := ch.BuildResponseXML("user-openid", "test content")

	// 解析 XML 验证内容
	var resp WechatResponse
	err := xml.Unmarshal([]byte(xmlStr), &resp)
	assert.NoError(t, err)

	assert.Equal(t, "user-openid", resp.ToUserName)
	assert.Equal(t, "bot-account", resp.FromUserName)
	assert.Equal(t, "text", resp.MsgType)
	assert.Equal(t, "test content", resp.Content)
	assert.Positive(t, resp.CreateTime, "CreateTime should be positive")
}

func TestChannel_SetToUserName(t *testing.T) {
	ch := NewChannel("initial-bot")
	ch.SetToUserName("new-bot")

	// 验证新的 toUserName 生效
	xmlStr := ch.BuildResponseXML("user", "test")
	var resp WechatResponse
	err := xml.Unmarshal([]byte(xmlStr), &resp)
	assert.NoError(t, err)
	assert.Equal(t, "new-bot", resp.FromUserName)
}
