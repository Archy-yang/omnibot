// Package wechatclaw 实现微信 ClawBot(官方 OpenClaw 微信插件)协议的 Go 客户端。
//
// 背景(31 号技术方案):微信 2026-03 推出的 ClawBot 走的是纯 HTTP/JSON 长轮询协议,
// 官方只提供 Node 插件(@tencent-weixin/openclaw-weixin,MIT),但协议本身可复用。
// 本包用 Go 原生实现扫码登录、长轮询收消息、发送文本,不依赖 OpenClaw 运行时。
//
// 分层约定:
//   - types.go  : 只描述线上协议结构(JSON tag 与官方插件保持一致)
//   - client.go : HTTP 细节(基址、鉴权头、错误码),不掺业务
//   - 业务(登录状态机 / 长轮询循环 / 通道适配)在 service 层与 channel.go
package wechatclaw

import (
	"encoding/json"
	"strings"
)

// FlexString 兼容 JSON 字符串与数字的 id 字段。
//
// 协议里 message_id / svr_id 是 uint64,直接在 JSON 里是数字;用 float64 解析会丢精度,
// 用 string 解析会报错。官方插件是在 JSON.parse 前把这两个字段加引号绕过的,
// Go 侧用自定义 Unmarshaler 原样保留数字文本即可。
type FlexString string

// UnmarshalJSON 接受 "123" / 123 / null 三种形态。
func (f *FlexString) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*f = ""
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*f = FlexString(s)
		return nil
	}
	*f = FlexString(raw)
	return nil
}

// String 返回原始文本。
func (f FlexString) String() string { return string(f) }

// 消息类型(协议 proto: MessageType)。
const (
	MessageTypeNone = 0
	MessageTypeUser = 1
	MessageTypeBot  = 2
)

// 消息状态(协议 proto: MessageState)。
const (
	MessageStateNew        = 0
	MessageStateGenerating = 1
	MessageStateFinish     = 2
)

// 消息内容类型(协议 proto: MessageItemType)。
const (
	ItemTypeNone  = 0
	ItemTypeText  = 1
	ItemTypeImage = 2
	ItemTypeVoice = 3
	ItemTypeFile  = 4
	ItemTypeVideo = 5
)

// 扫码状态(协议 get_qrcode_status 的 status 字段)。
const (
	QRStatusWait              = "wait"
	QRStatusScaned            = "scaned"
	QRStatusConfirmed         = "confirmed"
	QRStatusExpired           = "expired"
	QRStatusScanedButRedirect = "scaned_but_redirect"
	QRStatusNeedVerifyCode    = "need_verifycode"
	QRStatusVerifyCodeBlocked = "verify_code_blocked"
	QRStatusBindedRedirect    = "binded_redirect"
)

// baseInfo 每个请求都带的元信息(仅观测用,不参与鉴权)。
type baseInfo struct {
	ChannelVersion string `json:"channel_version,omitempty"`
	BotAgent       string `json:"bot_agent,omitempty"`
}

// QRCode 扫码登录第一步的返回。
type QRCode struct {
	// QRCode 是登录会话 id(后续 get_qrcode_status 用它查状态)。
	QRCode string `json:"qrcode"`
	// ImgContent 是「要被渲染成二维码的 URL」,不是图片二进制。
	ImgContent string `json:"qrcode_img_content"`
	Ret        int    `json:"ret"`
	ErrMsg     string `json:"errmsg"`
}

// QRStatus 扫码状态长轮询的返回。
type QRStatus struct {
	Status       string `json:"status"`
	BotToken     string `json:"bot_token"`
	BotID        string `json:"ilink_bot_id"`
	UserID       string `json:"ilink_user_id"`
	BaseURL      string `json:"baseurl"`
	RedirectHost string `json:"redirect_host"`
	Ret          int    `json:"ret"`
	ErrCode      int    `json:"errcode"`
	ErrMsg       string `json:"errmsg"`
}

// TextItem 文本内容。
type TextItem struct {
	Text string `json:"text"`
}

// MessageItem 单条消息内容项。本期只处理 TEXT。
type MessageItem struct {
	Type     int       `json:"type,omitempty"`
	TextItem *TextItem `json:"text_item,omitempty"`
}

// Message 协议中的 WeixinMessage。
type Message struct {
	Seq          int64         `json:"seq"`
	MessageID    FlexString    `json:"message_id"`
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	CreateTimeMS int64         `json:"create_time_ms"`
	SessionID    string        `json:"session_id"`
	GroupID      string        `json:"group_id"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ItemList     []MessageItem `json:"item_list"`
	ContextToken string        `json:"context_token"`
	RunID        string        `json:"run_id"`
}

// Text 取第一条文本内容;非文本消息返回空串。
func (m Message) Text() string {
	for _, item := range m.ItemList {
		if item.Type == ItemTypeText && item.TextItem != nil {
			return item.TextItem.Text
		}
	}
	return ""
}

// Updates getupdates 长轮询的返回。
type Updates struct {
	Ret                  int       `json:"ret"`
	ErrCode              int       `json:"errcode"`
	ErrMsg               string    `json:"errmsg"`
	Msgs                 []Message `json:"msgs"`
	GetUpdatesBuf        string    `json:"get_updates_buf"`
	LongPollingTimeoutMS int       `json:"longpolling_timeout_ms"`
}

// SendMessageResp sendmessage 的返回。
type SendMessageResp struct {
	MessageID FlexString `json:"message_id"`
	Ret       int        `json:"ret"`
	ErrMsg    string     `json:"errmsg"`
}

// CloseResp notifystart / notifystop 等空返回。
type CloseResp struct {
	Ret    int    `json:"ret"`
	ErrMsg string `json:"errmsg"`
}

// sendMessageReq sendmessage 请求体。
type sendMessageReq struct {
	Msg      sendMsg  `json:"msg"`
	BaseInfo baseInfo `json:"base_info"`
}

// sendMsg 出站消息体。from_user_id 留空(服务端按 token 识别 bot 身份)。
type sendMsg struct {
	FromUserID   string        `json:"from_user_id"`
	ToUserID     string        `json:"to_user_id"`
	ClientID     string        `json:"client_id"`
	MessageType  int           `json:"message_type"`
	MessageState int           `json:"message_state"`
	ItemList     []MessageItem `json:"item_list,omitempty"`
	ContextToken string        `json:"context_token,omitempty"`
}
