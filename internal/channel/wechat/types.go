// Package wechat 提供微信公众号通道的协议层适配:入站 XML 解析、
// 出站 XML 序列化。让 internal/api/wechat 的 handler 业务路径只需要
// 处理中性的 InboundMessage,XML 协议细节与 handler 解耦——和 feishu/web
// channel 同样的「业务出纯文本,通道层负责承载格式」模式。
package wechat

import (
	"encoding/xml"
	"errors"
)

// InboundMessage 中性的入站消息——handler 业务路径只看这个,不感知 XML。
// 字段沿用微信公众号文档命名,语义保留(便于不熟悉协议时查官方文档对照)。
type InboundMessage struct {
	ToUserName   string // 公众号微信号(响应时作为 FromUserName 回填)
	FromUserName string // 用户 OpenID(响应时作为 ToUserName 回填)
	CreateTime   int64
	MsgType      string // text / image / voice / video / shortvideo / location / link / event
	Content      string // text 消息内容
	MsgID        string // 普通消息有,event 没有
	PicURL       string // image 消息
	MediaID      string // image/voice/video
	Format       string // voice 格式
	ThumbMediaID string // video 缩略图
	LocationX    string
	LocationY    string
	Scale        string
	Label        string
	Title        string // link 标题
	Description  string // link 描述
	URL          string // link 链接
	Event        string // event 类型:subscribe / unsubscribe / CLICK 等
	EventKey     string
	Ticket       string
}

// rawMessage 微信公众号 XML 原始结构,只用于反序列化,不出包。
type rawMessage struct {
	XMLName      xml.Name `xml:"xml"`
	ToUserName   string   `xml:"ToUserName"`
	FromUserName string   `xml:"FromUserName"`
	CreateTime   int64    `xml:"CreateTime"`
	MsgType      string   `xml:"MsgType"`
	Content      string   `xml:"Content,omitempty"`
	MsgID        string   `xml:"MsgId,omitempty"`
	PicURL       string   `xml:"PicUrl,omitempty"`
	MediaID      string   `xml:"MediaId,omitempty"`
	Format       string   `xml:"Format,omitempty"`
	ThumbMediaID string   `xml:"ThumbMediaId,omitempty"`
	LocationX    string   `xml:"Location_X,omitempty"`
	LocationY    string   `xml:"Location_Y,omitempty"`
	Scale        string   `xml:"Scale,omitempty"`
	Label        string   `xml:"Label,omitempty"`
	Title        string   `xml:"Title,omitempty"`
	Description  string   `xml:"Description,omitempty"`
	URL          string   `xml:"Url,omitempty"`
	Event        string   `xml:"Event,omitempty"`
	EventKey     string   `xml:"EventKey,omitempty"`
	Ticket       string   `xml:"Ticket,omitempty"`
}

// Parse 把微信公众号回调 body(XML)解析为中性 InboundMessage。
// body 为空或非合法 XML 返回 error,handler 顶层据此回 400 即可。
func Parse(body []byte) (*InboundMessage, error) {
	if len(body) == 0 {
		return nil, errors.New("wechat: empty body")
	}
	var raw rawMessage
	if err := xml.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	return &InboundMessage{
		ToUserName:   raw.ToUserName,
		FromUserName: raw.FromUserName,
		CreateTime:   raw.CreateTime,
		MsgType:      raw.MsgType,
		Content:      raw.Content,
		MsgID:        raw.MsgID,
		PicURL:       raw.PicURL,
		MediaID:      raw.MediaID,
		Format:       raw.Format,
		ThumbMediaID: raw.ThumbMediaID,
		LocationX:    raw.LocationX,
		LocationY:    raw.LocationY,
		Scale:        raw.Scale,
		Label:        raw.Label,
		Title:        raw.Title,
		Description:  raw.Description,
		URL:          raw.URL,
		Event:        raw.Event,
		EventKey:     raw.EventKey,
		Ticket:       raw.Ticket,
	}, nil
}
