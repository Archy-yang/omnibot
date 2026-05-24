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

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/domain/user"
	chat "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/logger"

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

// callLLM 调用大模型生成回复（支持用户自定义配置和上下文）
func (h *Handler) callLLM(ctx context.Context, userID int64, userContent string, msgType string) string {
	start := time.Now()

	// 构建上下文消息列表
	var messages []llm.ChatMessage

	// 先加 system prompt（每次都加）
	messages = append(messages, llm.ChatMessage{
		Role:    conversation.RoleSystem,
		Content: systemPrompt,
	})

	// 如果有消息服务且有 userID，添加上下文历史
	if h.msgService != nil && userID > 0 {
		ctxMsgs, err := h.msgService.BuildContextMessages(ctx, userID, userContent)
		if err == nil {
			messages = append(messages, ctxMsgs...)
		}
		// err != nil 时降级：只有 system prompt + 当前消息
	} else {
		// 没有消息服务时，只加当前消息
		messages = append(messages, llm.ChatMessage{
			Role:    conversation.RoleUser,
			Content: userContent,
		})
	}

	// 检查是否有用户自定义配置
	if h.llmConfigService != nil && userID > 0 {
		apiKey, baseURL, model, hasCustom, err := h.llmConfigService.GetConfigForUser(userID)
		if err == nil && hasCustom {
			logger.InfoWithFields("Using user custom LLM config",
				zap.Int64("user_id", userID),
				zap.String("base_url", baseURL),
				zap.String("model", model),
			)
			customClient := llm.NewOpenAIProvider(apiKey, baseURL, model, 30*time.Second)
			resp, err := customClient.ChatCompletion(ctx, messages)
			if err == nil {
				logger.InfoWithFields("LLM call succeeded with user config",
					zap.String("msg_type", msgType),
					zap.Int64("user_id", userID),
					zap.Duration("duration", time.Since(start)),
				)
				return resp
			}
			logger.WarnWithFields("User config LLM call failed, falling back to system default",
				zap.String("msg_type", msgType),
				zap.Int64("user_id", userID),
				zap.String("error", err.Error()),
			)
		}
	}

	// 系统默认 LLM 调用
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

// UserService 用户服务接口
type UserService interface {
	GetOrCreateByOpenID(openID string) (*user.User, bool, error)
}

// Handler 微信公众号处理器
type Handler struct {
	config           Config
	llmClient        LLMClient
	userService      UserService
	llmConfigService userService.LLMConfigService
	msgService       chat.MessageService
	memoryService    memoryService.MemoryService
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
func NewHandler(
	config Config,
	llmClient LLMClient,
	userService UserService,
	optionalServices ...interface{},
) *Handler {
	handler := &Handler{
		config:      config,
		llmClient:   llmClient,
		userService: userService,
	}

	// 解析可选参数（支持 LLMConfigService 和 MessageService）
	for _, svc := range optionalServices {
		switch s := svc.(type) {
		case userLLMConfigService:
			handler.llmConfigService = s
		case chat.MessageService:
			handler.msgService = s
		case memoryService.MemoryService:
			handler.memoryService = s
		}
	}

	return handler
}

// userLLMConfigService 是为了避免 type switch 中的包名冲突
type userLLMConfigService = userService.LLMConfigService

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

	logger.InfoWithFields("Received wechat callback", zap.Int("body_length", len(body)))

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

	// 获取或创建用户（兜底机制）
	if h.userService != nil {
		_, _, err = h.userService.GetOrCreateByOpenID(msg.FromUserName)
		if err != nil {
			// 记录错误，但不影响消息正常处理
			logger.WarnWithFields("Failed to get or create user",
				zap.String("open_id", msg.FromUserName),
				zap.Error(err),
			)
		}
	}

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
		content := h.callLLM(context.Background(), 0, "用户发送了未知类型的消息", "unknown")
		return h.buildResponse(msg, content), nil
	}
}

// handleTextMessage 处理文本消息
func (h *Handler) handleTextMessage(msg *Message) (string, error) {
	// 获取 userID
	userID := h.getUserIDByOpenID(msg.FromUserName)

	// 处理配置命令
	if h.llmConfigService != nil {
		if reply, handled := h.handleConfigCommand(userID, msg.Content); handled {
			// 配置命令的回复也保存到上下文
			if h.msgService != nil && userID > 0 {
				// 先保存用户的命令消息
				h.msgService.SaveUserMessage(context.Background(), userID, msg.Content, msg.MsgID)
				// 再保存机器人的回复
				h.msgService.SaveAssistantMessage(context.Background(), userID, reply)
			}
			return h.buildResponse(msg, reply), nil
		}
	}

	// 处理记忆命令
	if h.memoryService != nil {
		if reply, handled := h.handleMemoryCommand(userID, msg.Content); handled {
			if h.msgService != nil && userID > 0 {
				h.msgService.SaveUserMessage(context.Background(), userID, msg.Content, msg.MsgID)
				h.msgService.SaveAssistantMessage(context.Background(), userID, reply)
			}
			return h.buildResponse(msg, reply), nil
		}
	}

	// 保存用户消息（去重）
	if h.msgService != nil && userID > 0 {
		err := h.msgService.SaveUserMessage(context.Background(), userID, msg.Content, msg.MsgID)
		if err == chat.ErrDuplicateMessage {
			// 重复消息，记录日志但继续执行
			logger.InfoWithFields("Duplicate message ignored",
				zap.Int64("user_id", userID),
				zap.String("msg_id", msg.MsgID),
			)
		}
		// 其他错误忽略，继续执行
	}

	// 调用 LLM（带上下文）
	content := h.callLLM(context.Background(), userID, msg.Content, "text")

	// 保存机器人回复
	if h.msgService != nil && userID > 0 {
		h.msgService.SaveAssistantMessage(context.Background(), userID, content)
	}

	return h.buildResponse(msg, content), nil
}

// getUserIDByOpenID 通过 OpenID 获取 userID
func (h *Handler) getUserIDByOpenID(openID string) int64 {
	if h.userService != nil {
		user, _, err := h.userService.GetOrCreateByOpenID(openID)
		if err == nil && user != nil {
			return user.ID
		}
	}
	return 0
}

// handleConfigCommand 处理配置命令
func (h *Handler) handleConfigCommand(userID int64, content string) (string, bool) {
	trimmed := strings.TrimSpace(content)

	switch {
	case trimmed == "#模型设置":
		return h.renderConfigMenu(userID), true
	case strings.HasPrefix(trimmed, "#设置Key "):
		key := strings.TrimPrefix(trimmed, "#设置Key ")
		return h.handleSetAPIKey(userID, strings.TrimSpace(key)), true
	case strings.HasPrefix(trimmed, "#设置地址 "):
		url := strings.TrimPrefix(trimmed, "#设置地址 ")
		return h.handleSetBaseURL(userID, strings.TrimSpace(url)), true
	case trimmed == "#我的配置":
		return h.handleGetConfig(userID), true
	case trimmed == "#重置模型":
		return h.handleClearConfig(userID), true
	}

	return "", false
}

func (h *Handler) handleMemoryCommand(userID int64, content string) (string, bool) {
	if h.memoryService == nil {
		return "", false
	}

	trimmed := strings.TrimSpace(content)
	isRememberCommand := trimmed == "#记住" || strings.HasPrefix(trimmed, "#记住 ") || strings.HasPrefix(trimmed, "#记住\t")
	isMemoryCommand := isRememberCommand || trimmed == "#我的记忆" || trimmed == "#清空记忆"
	if isMemoryCommand && userID <= 0 {
		return "服务暂时不可用，请稍后再试", true
	}

	switch {
	case trimmed == "#记住":
		return h.renderEmptyMemoryContentHint(), true
	case strings.HasPrefix(trimmed, "#记住 ") || strings.HasPrefix(trimmed, "#记住\t"):
		memoryContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "#记住"))
		memory, err := h.memoryService.Remember(context.Background(), userID, memoryContent)
		if err != nil {
			switch err {
			case memoryService.ErrEmptyContent:
				return h.renderEmptyMemoryContentHint(), true
			case memoryService.ErrContentTooLong:
				return "这条记忆太长了，请控制在 200 字以内。", true
			default:
				logger.ErrorWithFields("Failed to handle memory remember command",
					zap.Int64("user_id", userID),
					zap.String("operation", "memory_command_remember"),
					zap.Error(err),
				)
				return "服务暂时不可用，请稍后再试", true
			}
		}
		return fmt.Sprintf("已记住：%s\n\n提醒：请不要保存密码、API Key、身份证号等敏感信息。", memory.Content), true
	case trimmed == "#我的记忆":
		memories, err := h.memoryService.List(context.Background(), userID)
		if err != nil {
			logger.ErrorWithFields("Failed to handle memory list command",
				zap.Int64("user_id", userID),
				zap.String("operation", "memory_command_list"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		if len(memories) == 0 {
			return "我还没有长期记住任何信息。\n\n你可以这样告诉我：\n#记住 我偏好简洁直接的回答", true
		}
		var builder strings.Builder
		builder.WriteString("我目前记住了这些信息：\n\n")
		for i, memory := range memories {
			builder.WriteString(fmt.Sprintf("%d. %s", i+1, memory.Content))
			if i < len(memories)-1 {
				builder.WriteString("\n")
			}
		}
		return builder.String(), true
	case trimmed == "#清空记忆":
		if err := h.memoryService.Clear(context.Background(), userID); err != nil {
			logger.ErrorWithFields("Failed to handle memory clear command",
				zap.Int64("user_id", userID),
				zap.String("operation", "memory_command_clear"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		return "已清空你的全部长期记忆。", true
	}

	return "", false
}

func (h *Handler) renderEmptyMemoryContentHint() string {
	return "请在 #记住 后面输入要长期记住的内容，例如：\n#记住 我偏好简洁直接的回答"
}

func (h *Handler) renderConfigMenu(userID int64) string {
	statusText := "使用系统默认模型"
	if h.llmConfigService != nil && userID > 0 {
		view, err := h.llmConfigService.GetConfigView(userID)
		if err == nil && view.HasConfig {
			statusText = view.StatusText
		}
	}

	return fmt.Sprintf(`🔧 模型设置

请回复数字选择操作：
1️⃣ 设置 API Key
2️⃣ 设置 API 地址
3️⃣ 查看当前配置
4️⃣ 重置为系统默认

━━━━━━━━━━━━━━━━
当前状态：%s`, statusText)
}

func (h *Handler) handleSetAPIKey(userID int64, key string) string {
	err := h.llmConfigService.SetAPIKey(userID, key)
	if err != nil {
		return fmt.Sprintf("❌ 设置失败：%s", err.Error())
	}

	view, _ := h.llmConfigService.GetConfigView(userID)
	return fmt.Sprintf(`✅ API Key 设置成功！

当前配置：
- API Key：%s
- API 地址：%s
- 模型：%s
- 状态：%s`, view.APIKeyMasked, view.BaseURL, view.Model, view.StatusText)
}

func (h *Handler) handleSetBaseURL(userID int64, url string) string {
	err := h.llmConfigService.SetBaseURL(userID, url)
	if err != nil {
		return fmt.Sprintf("❌ 设置失败：%s", err.Error())
	}

	view, _ := h.llmConfigService.GetConfigView(userID)
	return fmt.Sprintf(`✅ API 地址设置成功！

当前配置：
- API Key：%s
- API 地址：%s
- 模型：%s
- 状态：%s`, view.APIKeyMasked, view.BaseURL, view.Model, view.StatusText)
}

func (h *Handler) handleGetConfig(userID int64) string {
	view, err := h.llmConfigService.GetConfigView(userID)
	if err != nil {
		return "❌ 获取配置失败"
	}

	if !view.HasConfig {
		return fmt.Sprintf(`📋 当前配置

状态：%s

发送「#模型设置」可配置自定义模型`, view.StatusText)
	}

	return fmt.Sprintf(`📋 当前配置

- API Key：%s
- API 地址：%s
- 模型：%s
- 状态：%s

发送「#重置模型」可恢复为系统默认`, view.APIKeyMasked, view.BaseURL, view.Model, view.StatusText)
}

func (h *Handler) handleClearConfig(userID int64) string {
	err := h.llmConfigService.ClearConfig(userID)
	if err != nil {
		return "❌ 重置失败"
	}

	return `✅ 已重置为系统默认模型

你的自定义配置已清除，将使用系统提供的模型服务。`
}

// handleImageMessage 处理图片消息
func (h *Handler) handleImageMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了一张图片", "image")
	return h.buildResponse(msg, content), nil
}

// handleVoiceMessage 处理语音消息
func (h *Handler) handleVoiceMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了一条语音消息", "voice")
	return h.buildResponse(msg, content), nil
}

// handleVideoMessage 处理视频消息
func (h *Handler) handleVideoMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了一条视频消息", "video")
	return h.buildResponse(msg, content), nil
}

// handleShortVideoMessage 处理小视频消息
func (h *Handler) handleShortVideoMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了一条小视频消息", "shortvideo")
	return h.buildResponse(msg, content), nil
}

// handleLocationMessage 处理位置消息
func (h *Handler) handleLocationMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了位置信息", "location")
	return h.buildResponse(msg, content), nil
}

// handleLinkMessage 处理链接消息
func (h *Handler) handleLinkMessage(msg *Message) (string, error) {
	content := h.callLLM(context.Background(), 0, "用户发送了一个链接", "link")
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
		content := h.callLLM(context.Background(), 0, "用户触发了未知事件", "event_unknown")
		return h.buildResponse(msg, content), nil
	}
}

// handleSubscribeEvent 处理订阅事件
func (h *Handler) handleSubscribeEvent(msg *Message) (string, error) {
	// 关注事件 - 创建用户
	if h.userService != nil {
		_, isNew, err := h.userService.GetOrCreateByOpenID(msg.FromUserName)
		if err != nil {
			logger.WarnWithFields("Failed to create user on subscribe",
				zap.String("open_id", msg.FromUserName),
				zap.Error(err),
			)
		}
		if isNew {
			logger.InfoWithFields("New user created on subscribe",
				zap.String("open_id", msg.FromUserName),
			)
		}
	}

	content := h.callLLM(context.Background(), 0, "用户刚刚关注了公众号，请生成友好的欢迎语", "subscribe")
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
	content := h.callLLM(context.Background(), 0, fmt.Sprintf("用户点击了菜单按钮：%s", msg.EventKey), "click")
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
