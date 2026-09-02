package wechat

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	channelwechat "omnibot/internal/channel/wechat"
	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	chat "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	userSvc "omnibot/internal/service/user"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const systemPrompt = "你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"

// v2.3: 微信绑定相关常量
var weChatBindCodeRe = regexp.MustCompile(`^绑定 (\d{6})$`)

const (
	// 绑定结果回复(PRD 5.1)
	bindSuccessReplyWeChat         = "绑定成功!现在可以在微信跟我聊了"
	bindCodeInvalidReply           = "绑定码无效或已过期,请在 web 端重新获取"
	weChatAlreadyBoundReply        = "该微信号已绑定其他账号"
	accountWeChatAlreadyBoundReply = "你的账号已绑定微信,如需更换请稍后(暂不支持)"
	fallbackReplyWeChat            = "服务暂时不可用,请稍后再试"
	// 未绑定引导(PRD 5.4)
	unboundGuideWeChat = "你还没有绑定 OmniBot 账号。请先在 web 端登录,在设置里获取绑定码,然后在这里发送「绑定 + 空格 + 绑定码」(例如 绑定 123456)完成绑定。"
	// 关注欢迎语 + 绑定引导(PRD 5.4)
	subscribeGuideWeChat = "欢迎关注 OmniBot!你还没有绑定账号,请先在 web 端登录并获取绑定码,然后发送「绑定 + 空格 + 绑定码」完成绑定,绑定后我们就可以聊天啦。"
)

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

// LLMClient 大模型客户端接口
type LLMClient interface {
	ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error)
}

// BindingService 账号绑定服务接口(v2.3)。
// 微信入口从自动建号改为绑定模型:先解析绑定码 -> BindChannel;
// 否则 ResolveUserID 查已绑身份,未绑回引导不建号(PRD 5.4)。
type BindingService interface {
	BindChannel(channelType, code, openID string) error
	ResolveUserID(channelType, openID string) (userID int64, bound bool, err error)
}

// Handler 微信公众号处理器。
//
// v1.9 起 Handler 业务路径只处理 channelwechat.InboundMessage 中性结构,
// XML 解析/序列化在 HandleMessage 顶层通过 channelwechat.Parse / wechatChannel.BuildResponseXML
// 完成,handler 本身与 XML 协议解耦——与 feishu / web handler 对齐。
type Handler struct {
	config           Config
	wechatChannel    *channelwechat.Channel // v1.9 注入,负责出站 XML 序列化
	llmClient        LLMClient
	bindingService   BindingService
	llmConfigService userSvc.LLMConfigService
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

// NewHandler 创建微信处理器。
//
// v1.9 起 wechatChannel 由调用方注入(通常 routes.go 装配)——若调用方未传入,
// HandleMessage 内部会用默认 channelwechat.NewChannel() 兜底,保证旧测试构造路径
// (NewHandler(config, llmClient, userService, optionalServices...)) 不变。
func NewHandler(
	config Config,
	llmClient LLMClient,
	bindingSvc BindingService,
	optionalServices ...interface{},
) *Handler {
	handler := &Handler{
		config:         config,
		llmClient:      llmClient,
		bindingService: bindingSvc,
		wechatChannel:  channelwechat.NewChannel(),
	}

	// 解析可选参数(支持 LLMConfigService / MessageService / MemoryService / *channelwechat.Channel)
	for _, svc := range optionalServices {
		switch s := svc.(type) {
		case userLLMConfigService:
			handler.llmConfigService = s
		case chat.MessageService:
			handler.msgService = s
		case memoryService.MemoryService:
			handler.memoryService = s
		case *channelwechat.Channel:
			handler.wechatChannel = s
		}
	}

	return handler
}

// userLLMConfigService 是为了避免 type switch 中的包名冲突
type userLLMConfigService = userSvc.LLMConfigService

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

// HandleMessage 处理微信消息。XML 协议在此层进出,业务路径只看中性 InboundMessage。
func (h *Handler) HandleMessage(c *gin.Context) {
	// 读取请求体
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.Error("Failed to read request body", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to read request body")
		return
	}

	logger.InfoWithFields("Received wechat callback", zap.Int("body_length", len(body)))

	// 解析 XML 消息(协议层,channel 包负责)
	in, err := channelwechat.Parse(body)
	if err != nil {
		logger.ErrorWithFields("Failed to parse message", zap.Error(err))
		c.String(http.StatusBadRequest, "Failed to parse message")
		return
	}

	// 记录收到的消息
	logger.InfoWithFields("Received wechat message",
		zap.String("type", in.MsgType),
		zap.String("from_user_name", in.FromUserName),
		zap.String("to_user_name", in.ToUserName),
		zap.String("msg_id", in.MsgID),
	)

	// v2.3: 身份解析下沉到 handleTextMessage(绑定码解析 + 已绑解析 + 未绑引导),
	// 不再在顶层兜底建号。

	// 分发消息处理(业务路径,只产纯文本)
	content, err := h.dispatchMessage(in)
	if err != nil {
		logger.ErrorWithFields("Failed to dispatch message", zap.Error(err))
		c.String(http.StatusInternalServerError, "Failed to dispatch message")
		return
	}

	// content 为空表示「不回复」(如 unsubscribe / view 事件),按微信协议直接空响应
	if content == "" {
		c.String(http.StatusOK, "")
		return
	}

	// 包装成 XML 响应(协议层,channel 包负责)
	c.Header("Content-Type", "application/xml")
	c.String(http.StatusOK, h.wechatChannel.BuildResponseXML(in.FromUserName, in.ToUserName, content))
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

// dispatchMessage 分发消息处理。返回纯文本回复内容;空串表示「不回复」。
func (h *Handler) dispatchMessage(in *channelwechat.InboundMessage) (string, error) {
	// 根据消息类型分发处理
	switch in.MsgType {
	case "text":
		return h.handleTextMessage(in)
	case "image":
		return h.handleImageMessage(in)
	case "voice":
		return h.handleVoiceMessage(in)
	case "video":
		return h.handleVideoMessage(in)
	case "shortvideo":
		return h.handleShortVideoMessage(in)
	case "location":
		return h.handleLocationMessage(in)
	case "link":
		return h.handleLinkMessage(in)
	case "event":
		return h.handleEventMessage(in)
	default:
		logger.WarnWithFields("Unknown message type", zap.String("type", in.MsgType))
		return h.callLLM(context.Background(), 0, "用户发送了未知类型的消息", "unknown"), nil
	}
}

// handleTextMessage 处理文本消息
func (h *Handler) handleTextMessage(in *channelwechat.InboundMessage) (string, error) {
	// v2.3: 先看是否绑定码格式
	if code, ok := parseWeChatBindCode(in.Content); ok {
		return h.handleBindCode(in.FromUserName, code), nil
	}

	// 解析已绑定身份(未绑不建号)
	userID, bound := h.getUserIDByOpenID(in.FromUserName)
	if !bound {
		// 未绑定:回引导,不进对话/命令(PRD 5.4)
		return unboundGuideWeChat, nil
	}

	// 处理配置命令(仅已绑定用户可用)
	if h.llmConfigService != nil {
		if reply, handled := h.handleConfigCommand(userID, in.Content); handled {
			// 配置命令的回复也保存到上下文
			if h.msgService != nil && userID > 0 {
				// 先保存用户的命令消息
				h.msgService.SaveUserMessage(context.Background(), userID, in.Content, in.MsgID)
				// 再保存机器人的回复
				h.msgService.SaveAssistantMessage(context.Background(), userID, reply)
			}
			return reply, nil
		}
	}

	// 处理记忆命令
	if h.memoryService != nil {
		if reply, handled := h.handleMemoryCommand(userID, in.Content); handled {
			if h.msgService != nil && userID > 0 {
				h.msgService.SaveUserMessage(context.Background(), userID, in.Content, in.MsgID)
				h.msgService.SaveAssistantMessage(context.Background(), userID, reply)
			}
			return reply, nil
		}
	}

	// 保存用户消息（去重）
	if h.msgService != nil && userID > 0 {
		err := h.msgService.SaveUserMessage(context.Background(), userID, in.Content, in.MsgID)
		if err == chat.ErrDuplicateMessage {
			// 重复消息，记录日志但继续执行
			logger.InfoWithFields("Duplicate message ignored",
				zap.Int64("user_id", userID),
				zap.String("msg_id", in.MsgID),
			)
		}
		// 其他错误忽略，继续执行
	}

	// 调用 LLM（带上下文）
	content := h.callLLM(context.Background(), userID, in.Content, "text")

	// 保存机器人回复
	if h.msgService != nil && userID > 0 {
		h.msgService.SaveAssistantMessage(context.Background(), userID, content)
	}

	return content, nil
}

// getUserIDByOpenID 解析已绑定的微信身份。
// v2.3: 未绑定不再建号,返回 (0, false),由调用方决定回引导。
func (h *Handler) getUserIDByOpenID(openID string) (int64, bool) {
	if h.bindingService != nil {
		uid, bound, err := h.bindingService.ResolveUserID("wechat", openID)
		if err == nil && bound {
			return uid, true
		}
	}
	return 0, false
}

// parseWeChatBindCode 解析「绑定 <6位数字>」格式(PRD 4.3,与飞书同款)。
func parseWeChatBindCode(text string) (string, bool) {
	m := weChatBindCodeRe.FindStringSubmatch(strings.TrimSpace(text))
	if len(m) != 2 {
		return "", false
	}
	return m[1], true
}

// handleBindCode 处理微信端绑定码提交,按 PRD 5.1 映射回复文案。
func (h *Handler) handleBindCode(openID, code string) string {
	err := h.bindingService.BindChannel("wechat", code, openID)
	reply := bindSuccessReplyWeChat
	switch {
	case err == nil:
		// 成功
	case errors.Is(err, userSvc.ErrCodeInvalid):
		reply = bindCodeInvalidReply
	case errors.Is(err, userSvc.ErrChannelAlreadyBound):
		reply = weChatAlreadyBoundReply
	case errors.Is(err, userSvc.ErrAccountAlreadyBound):
		reply = accountWeChatAlreadyBoundReply
	default:
		logger.ErrorWithFields("wechat: bind failed",
			zap.String("open_id", openID),
			zap.Error(err),
		)
		reply = fallbackReplyWeChat
	}
	return reply
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
	isDeleteCommand := trimmed == "#删除记忆" || strings.HasPrefix(trimmed, "#删除记忆 ") || strings.HasPrefix(trimmed, "#删除记忆\t")
	isMemoryCommand := isRememberCommand || isDeleteCommand || trimmed == "#我的记忆" || trimmed == "#清空记忆"
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
			// 来源标记(PRD AC4.3):自动沉淀与手动交代区分
			tag := ""
			if memory.Source == memorydomain.MemorySourceAuto {
				tag = "（自动记忆）"
			}
			builder.WriteString(fmt.Sprintf("%d. %s%s（记录于 %s）", i+1, memory.Content, tag, memory.CreatedAt.Format("2006-01-02")))
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
	case trimmed == "#删除记忆" || strings.HasPrefix(trimmed, "#删除记忆 ") || strings.HasPrefix(trimmed, "#删除记忆\t"):
		indexText := strings.TrimSpace(strings.TrimPrefix(trimmed, "#删除记忆"))
		index, err := strconv.Atoi(indexText)
		if err != nil || index <= 0 {
			return "请发送 #删除记忆 序号，例如：#删除记忆 2", true
		}

		memories, err := h.memoryService.List(context.Background(), userID)
		if err != nil {
			logger.ErrorWithFields("Failed to list memories before delete command",
				zap.Int64("user_id", userID),
				zap.String("operation", "memory_command_delete_list"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		if index > len(memories) {
			return "记忆序号不存在，请发送 #我的记忆 查看当前列表。", true
		}

		selected := memories[index-1]
		deleted, err := h.memoryService.Delete(context.Background(), userID, selected.ID)
		if err != nil {
			logger.ErrorWithFields("Failed to handle memory delete command",
				zap.Int64("user_id", userID),
				zap.Int64("memory_id", selected.ID),
				zap.String("operation", "memory_command_delete"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		if !deleted {
			return "记忆序号不存在，请发送 #我的记忆 查看当前列表。", true
		}
		return fmt.Sprintf("已删除记忆：%s", selected.Content), true
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
func (h *Handler) handleImageMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了一张图片", "image"), nil
}

// handleVoiceMessage 处理语音消息
func (h *Handler) handleVoiceMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了一条语音消息", "voice"), nil
}

// handleVideoMessage 处理视频消息
func (h *Handler) handleVideoMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了一条视频消息", "video"), nil
}

// handleShortVideoMessage 处理小视频消息
func (h *Handler) handleShortVideoMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了一条小视频消息", "shortvideo"), nil
}

// handleLocationMessage 处理位置消息
func (h *Handler) handleLocationMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了位置信息", "location"), nil
}

// handleLinkMessage 处理链接消息
func (h *Handler) handleLinkMessage(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, "用户发送了一个链接", "link"), nil
}

// handleEventMessage 处理事件消息
func (h *Handler) handleEventMessage(in *channelwechat.InboundMessage) (string, error) {
	switch in.Event {
	case "subscribe":
		return h.handleSubscribeEvent(in)
	case "unsubscribe":
		return h.handleUnsubscribeEvent(in)
	case "CLICK":
		return h.handleClickEvent(in)
	case "VIEW":
		return h.handleViewEvent(in)
	default:
		logger.WarnWithFields("Unknown event type", zap.String("event", in.Event))
		return h.callLLM(context.Background(), 0, "用户触发了未知事件", "event_unknown"), nil
	}
}

// handleSubscribeEvent 处理订阅事件
// v2.3: 关注不再自动建号,回欢迎语 + 绑定引导(PRD 5.4)。
func (h *Handler) handleSubscribeEvent(in *channelwechat.InboundMessage) (string, error) {
	return subscribeGuideWeChat, nil
}

// handleUnsubscribeEvent 处理取消订阅事件
func (h *Handler) handleUnsubscribeEvent(in *channelwechat.InboundMessage) (string, error) {
	// 记录取消订阅事件,微信端不需要回复
	logger.InfoWithFields("User unsubscribe", zap.String("user_id", in.FromUserName))
	return "", nil
}

// handleClickEvent 处理点击事件
func (h *Handler) handleClickEvent(in *channelwechat.InboundMessage) (string, error) {
	return h.callLLM(context.Background(), 0, fmt.Sprintf("用户点击了菜单按钮：%s", in.EventKey), "click"), nil
}

// handleViewEvent 处理视图事件
func (h *Handler) handleViewEvent(in *channelwechat.InboundMessage) (string, error) {
	// 记录视图事件,不需要回复消息
	logger.InfoWithFields("User view menu",
		zap.String("user_id", in.FromUserName),
		zap.String("event_key", in.EventKey),
	)
	return "", nil
}
