package wechat

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"wechat-intelligent-bot/internal/client/llm"
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

const systemPrompt = "你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"

// callLLM 调用大模型生成回复
func (h *Handler) callLLM(ctx context.Context, userContent string, msgType string) string {
	start := time.Now()
	messages := []llm.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userContent},
	}

	resp, err := h.llmClient.ChatCompletion(ctx, messages)
	if err != nil {
		logger.WarnWithFields("LLM call failed",
			zap.String("msg_type", msgType),
			zap.String("error", err.Error()),
			zap.Duration("duration", time.Since(start)),
		)
		return "服务暂时不可用，请稍后再试"
	}

	logger.InfoWithFields("LLM call succeeded",
		zap.String("msg_type", msgType),
		zap.Duration("duration", time.Since(start)),
	)
	return resp
}

// parseMessage 解析XML消息
func parseMessage(xmlContent string) (*Message, error) {
	var msg Message
	if err := xml.Unmarshal([]byte(xmlContent), &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// LLMClient 大模型客户端接口
type LLMClient interface {
	ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error)
}

// Handler 微信公众号处理器
type Handler struct {
	config    Config
	llmClient LLMClient
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
func NewHandler(config Config, llmClient LLMClient) *Handler {
	return &Handler{
		config:    config,
		llmClient: llmClient,
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
		logger.WarnWithFields("Invalid signature",
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

	// 打印原始请求体内容
	logger.InfoWithFields("Received raw wechat message", zap.String("body", string(body)))

	// 解析XML消息
	msg, err := parseMessage(string(body))
	if err != nil {
		logger.ErrorWithFields("Failed to parse message", zap.Error(err))
		c.String(http.StatusBadRequest, "Failed to parse message")
		return
	}

	// 记录收到的消息
	logger.InfoWithFields("Received wechat message",
		zap.String("type", msg.MsgType),
		zap.String("from_user_name", msg.FromUserName),
		zap.String("to_user_name", msg.ToUserName),
		zap.String("msg_id", msg.MsgID),
	)

	// 分发消息处理
	response, err := h.dispatchMessage(msg)
	if err != nil {
		logger.ErrorWithFields("Failed to dispatch message", zap.Error(err))
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
		logger.WarnWithFields("Unknown message type", zap.String("type", msg.MsgType))
		content := h.callLLM(context.Background(), "用户发送了未知类型的消息", "unknown")
		return h.buildResponse(msg, content), nil
	}
}

// handleTextMessage 处理文本消息
func (h *Handler) handleTextMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), msg.Content, "text")
	return h.buildResponse(msg, content), nil
}

// handleImageMessage 处理图片消息
func (h *Handler) handleImageMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了一张图片", "image")
	return h.buildResponse(msg, content), nil
}

// handleVoiceMessage 处理语音消息
func (h *Handler) handleVoiceMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了一条语音消息", "voice")
	return h.buildResponse(msg, content), nil
}

// handleVideoMessage 处理视频消息
func (h *Handler) handleVideoMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了一条视频消息", "video")
	return h.buildResponse(msg, content), nil
}

// handleShortVideoMessage 处理小视频消息
func (h *Handler) handleShortVideoMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了一条小视频消息", "shortvideo")
	return h.buildResponse(msg, content), nil
}

// handleLocationMessage 处理位置消息
func (h *Handler) handleLocationMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了位置信息", "location")
	return h.buildResponse(msg, content), nil
}

// handleLinkMessage 处理链接消息
func (h *Handler) handleLinkMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户发送了一个链接", "link")
	return h.buildResponse(msg, content), nil
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
		logger.WarnWithFields("Unknown event type", zap.String("event", msg.Event))
		content := h.callLLM(context.Background(), "用户触发了未知事件", "event_unknown")
		return h.buildResponse(msg, content), nil
	}
}

// handleSubscribeEvent 处理订阅事件
func (h *Handler) handleSubscribeEvent(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), "用户刚刚关注了公众号，请生成友好的欢迎语", "subscribe")
	return h.buildResponse(msg, content), nil
}

// buildResponse 构建响应消息
func (h *Handler) buildResponse(msg *Message, content string) string {
	now := time.Now().Unix()
	response := fmt.Sprintf(`<xml>
	<ToUserName><![CDATA[%s]]></ToUserName>
	<FromUserName><![CDATA[%s]]></FromUserName>
	<CreateTime>%d</CreateTime>
	<MsgType><![CDATA[text]]></MsgType>
	<Content><![CDATA[%s]]></Content>
	</xml>`, msg.FromUserName, msg.ToUserName, now, content)
	return response
}

// handleUnsubscribeEvent 处理取消订阅事件
func (h *Handler) handleUnsubscribeEvent(msg *Message) (string, error) {
	// 记录取消订阅事件
	logger.InfoWithFields("User unsubscribe", zap.String("user_id", msg.FromUserName))
	// 不需要回复消息
	return "", nil
}

// handleClickEvent 处理点击事件
func (h *Handler) handleClickEvent(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), fmt.Sprintf("用户点击了菜单按钮：%s", msg.EventKey), "click")
	return h.buildResponse(msg, content), nil
}

// handleViewEvent 处理视图事件
func (h *Handler) handleViewEvent(msg *Message) (string, error) {
	// 记录视图事件
	logger.InfoWithFields("User view menu",
		zap.String("user_id", msg.FromUserName),
		zap.String("event_key", msg.EventKey),
	)
	// 不需要回复消息
	return "", nil
}
