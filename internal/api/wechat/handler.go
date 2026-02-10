package wechat

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"wechat-intelligent-bot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Message 微信消息结构体
type Message struct {
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
	Url          string   `xml:"Url,omitempty"`
	Event        string   `xml:"Event,omitempty"`
	EventKey     string   `xml:"EventKey,omitempty"`
	Ticket       string   `xml:"Ticket,omitempty"`
}

// parseMessage 解析XML消息
func parseMessage(xmlContent string) (*Message, error) {
	var msg Message
	if err := xml.Unmarshal([]byte(xmlContent), &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// Handler 微信公众号处理器
type Handler struct {
	config Config
}

// Config 微信配置
type Config struct {
	AppID          string `mapstructure:"app_id"`
	AppSecret      string `mapstructure:"app_secret"`
	Token          string `mapstructure:"token"`
	EncodingAESKey string `mapstructure:"encoding_aes_key"`
	CallbackURL    string `mapstructure:"callback_url"`
}

// NewHandler 创建微信处理器
func NewHandler(config Config) *Handler {
	return &Handler{
		config: config,
	}
}

// Verify 微信服务器验证
func (h *Handler) Verify(c *gin.Context) {
	// 获取微信服务器发送的参数
	signature := c.Query("signature")
	timestamp := c.Query("timestamp")
	nonce := c.Query("nonce")
	echostr := c.Query("echostr")

	// 验证参数
	if signature == "" || timestamp == "" || nonce == "" || echostr == "" {
		c.String(http.StatusBadRequest, "Invalid parameters")
		return
	}

	// 验证签名
	if !h.verifySignature(signature, timestamp, nonce) {
		logger.Warn("Invalid signature",
			zap.String("signature", signature),
			zap.String("timestamp", timestamp),
			zap.String("nonce", nonce),
		)
		c.String(http.StatusForbidden, "Invalid signature")
		return
	}

	// 返回echostr
	c.String(http.StatusOK, echostr)
}

// HandleMessage 处理微信消息
func (h *Handler) HandleMessage(c *gin.Context) {
	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Failed to read request body", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to read request body")
		return
	}

	// 解析XML消息
	msg, err := parseMessage(string(body))
	if err != nil {
		logger.Error("Failed to parse message", zap.Error(err))
		c.String(http.StatusBadRequest, "Failed to parse message")
		return
	}

	// 记录收到的消息
	logger.Info("Received wechat message",
		zap.String("type", msg.MsgType),
		zap.String("from_user_name", msg.FromUserName),
		zap.String("to_user_name", msg.ToUserName),
		zap.String("msg_id", msg.MsgID),
	)

	// 分发消息处理
	response, err := h.dispatchMessage(msg)
	if err != nil {
		logger.Error("Failed to dispatch message", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to dispatch message")
		return
	}

	// 返回响应
	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, response)
}

// verifySignature 验证微信签名
func (h *Handler) verifySignature(signature, timestamp, nonce string) bool {
	// 1. 将token、timestamp、nonce三个参数进行字典序排序
	strs := []string{h.config.Token, timestamp, nonce}
	sort.Strings(strs)

	// 2. 将三个参数字符串拼接成一个字符串进行sha1加密
	str := strings.Join(strs, "")
	sha1 := sha1.New()
	sha1.Write([]byte(str))
	hash := hex.EncodeToString(sha1.Sum(nil))

	// 3. 开发者获得加密后的字符串可与signature对比，标识该请求来源于微信
	return hash == signature
}

// dispatchMessage 分发消息处理
func (h *Handler) dispatchMessage(msg *Message) (string, error) {
	// 根据消息类型分发处理
	switch msg.MsgType {
	case "text":
		return h.handleTextMessage(msg)
	case "image":
		return h.handleImageMessage(msg)
	case "voice":
		return h.handleVoiceMessage(msg)
	case "video":
		return h.handleVideoMessage(msg)
	case "shortvideo":
		return h.handleShortVideoMessage(msg)
	case "location":
		return h.handleLocationMessage(msg)
	case "link":
		return h.handleLinkMessage(msg)
	case "event":
		return h.handleEventMessage(msg)
	default:
		logger.Warn("Unknown message type", zap.String("type", msg.MsgType))
		return h.defaultResponse(msg), nil
	}
}

// handleTextMessage 处理文本消息
func (h *Handler) handleTextMessage(msg *Message) (string, error) {
	// 简单回复文本消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的消息：%s]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime, msg.Content)
	return response, nil
}

// handleImageMessage 处理图片消息
func (h *Handler) handleImageMessage(msg *Message) (string, error) {
	// 简单回复图片消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的图片消息]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response, nil
}

// handleVoiceMessage 处理语音消息
func (h *Handler) handleVoiceMessage(msg *Message) (string, error) {
	// 简单回复语音消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的语音消息]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response, nil
}

// handleVideoMessage 处理视频消息
func (h *Handler) handleVideoMessage(msg *Message) (string, error) {
	// 简单回复视频消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的视频消息]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response, nil
}

// handleShortVideoMessage 处理小视频消息
func (h *Handler) handleShortVideoMessage(msg *Message) (string, error) {
	// 简单回复小视频消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的小视频消息]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response, nil
}

// handleLocationMessage 处理位置消息
func (h *Handler) handleLocationMessage(msg *Message) (string, error) {
	// 简单回复位置消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的位置消息，纬度：%s，经度：%s]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime, msg.LocationX, msg.LocationY)
	return response, nil
}

// handleLinkMessage 处理链接消息
func (h *Handler) handleLinkMessage(msg *Message) (string, error) {
	// 简单回复链接消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[收到您的链接消息，标题：%s]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime, msg.Title)
	return response, nil
}

// handleEventMessage 处理事件消息
func (h *Handler) handleEventMessage(msg *Message) (string, error) {
	// 根据事件类型处理
	switch msg.Event {
	case "subscribe":
		return h.handleSubscribeEvent(msg)
	case "unsubscribe":
		return h.handleUnsubscribeEvent(msg)
	case "CLICK":
		return h.handleClickEvent(msg)
	case "VIEW":
		return h.handleViewEvent(msg)
	default:
		logger.Warn("Unknown event type", zap.String("event", msg.Event))
		return h.defaultResponse(msg), nil
	}
}

// handleSubscribeEvent 处理订阅事件
func (h *Handler) handleSubscribeEvent(msg *Message) (string, error) {
	// 回复欢迎消息
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[您好！欢迎关注我们的公众号，很高兴为您服务！]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response, nil
}

// handleUnsubscribeEvent 处理取消订阅事件
func (h *Handler) handleUnsubscribeEvent(msg *Message) (string, error) {
	// 记录取消订阅事件
	logger.Info("User unsubscribe", zap.String("user_id", msg.FromUserName))
	// 不需要回复消息
	return "", nil
}

// handleClickEvent 处理点击事件
func (h *Handler) handleClickEvent(msg *Message) (string, error) {
	// 简单回复点击事件
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[您点击了菜单：%s]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime, msg.EventKey)
	return response, nil
}

// handleViewEvent 处理视图事件
func (h *Handler) handleViewEvent(msg *Message) (string, error) {
	// 记录视图事件
	logger.Info("User view menu",
		zap.String("user_id", msg.FromUserName),
		zap.String("event_key", msg.EventKey),
	)
	// 不需要回复消息
	return "", nil
}

// defaultResponse 默认回复
func (h *Handler) defaultResponse(msg *Message) string {
	response := fmt.Sprintf(`<xml>
<ToUserName><![CDATA[%s]]></ToUserName>
<FromUserName><![CDATA[%s]]></FromUserName>
<CreateTime>%d</CreateTime>
<MsgType><![CDATA[text]]></MsgType>
<Content><![CDATA[暂不支持此消息类型，请发送文本消息！]]></Content>
</xml>`, msg.FromUserName, msg.ToUserName, msg.CreateTime)
	return response
}
