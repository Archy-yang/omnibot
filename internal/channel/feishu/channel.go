package feishu

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	domainchannel "omnibot/internal/domain/channel"
	"omnibot/pkg/logger"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
	"go.uber.org/zap"
)

// 编译期保证 Channel 满足 MessageChannel 接口
var _ domainchannel.MessageChannel = (*Channel)(nil)

// Config 飞书 channel 运行所需的最小配置——通常从 pkg/config.FeishuConfig 拷过来,
// 这里独立定义避免 channel 包反过来依赖 pkg/config(分层洁癖)。
type Config struct {
	AppID     string
	AppSecret string
	Enabled   bool
}

// Channel 飞书消息渠道。Start 启动长连接 goroutine 接收消息;实现 MessageChannel
// 用于和现有 web/wechat channel 风格统一(目前 SendText/SendReply 走 Sender 直接调 SDK,
// channel factory 内 dispatch 暂未用到这些方法,留作扩展)。
type Channel struct {
	cfg     Config
	handler *MessageHandler
	sender  Sender
	starter Starter // 可注入,测试用 mock 跳过真实 SDK 连接
}

// Starter 长连接启动器抽象。生产实现是 larkws.Client.Start(阻塞);测试可注入 mock。
type Starter interface {
	Start(ctx context.Context) error
}

// NewChannel 创建飞书 channel。
//   - cfg.Enabled=false 时,Start 直接返回 nil 不连(开发态友好)
//   - cfg.Enabled=true 但凭证为空,Start 返回 error(早失败,别静默)
//
// 默认 starter 用飞书官方 ws SDK + EventDispatcher 注册 OnP2MessageReceiveV1。
// 测试构造 channel 时通过 WithStarter 覆盖。
func NewChannel(cfg Config, handler *MessageHandler, sender Sender, opts ...Option) *Channel {
	c := &Channel{cfg: cfg, handler: handler, sender: sender}
	for _, opt := range opts {
		opt(c)
	}
	if c.starter == nil && cfg.Enabled {
		c.starter = newLarkWSStarter(cfg, handler)
	}
	return c
}

// Option 配置 Channel 的可选项,主要给测试用。
type Option func(*Channel)

// WithStarter 注入自定义长连接启动器(测试 mock SDK 用)。
func WithStarter(s Starter) Option {
	return func(c *Channel) { c.starter = s }
}

// ChannelType 渠道类型,值与 GetOrCreateByChannel 的 channelType 参数一致。
func (c *Channel) ChannelType() string { return "feishu" }

// IsAsync 飞书 IM 消息是异步的(发消息通过 API,不是请求响应模型)。
func (c *Channel) IsAsync() bool { return true }

// SendText 通过 Sender 给 open_id 发文本消息。
func (c *Channel) SendText(channelUserID, content string) error {
	if c.sender == nil {
		return errors.New("feishu: sender not configured")
	}
	return c.sender.SendText(context.Background(), channelUserID, content)
}

// SendReply 飞书 v1.6 暂不实现「回复某条」语义,等价于发新消息。
// 未来若需引用回复(reply_in_thread)再扩展。
func (c *Channel) SendReply(channelMessageID, channelUserID, content string) error {
	return c.SendText(channelUserID, content)
}

// Start 启动长连接。阻塞直到 ctx 取消或 starter 返回(SDK 自带重连,正常不返回)。
// 调用方应在 goroutine 中调用,带 recover 兜底。
//
// 行为矩阵:
//   - cfg.Enabled=false → 立即返回 nil,不报错,日志 info
//   - cfg.Enabled=true 但凭证空 → 返回 ErrMissingCredentials
//   - cfg.Enabled=true 且凭证齐 → 调 starter.Start(阻塞)
func (c *Channel) Start(ctx context.Context) error {
	if !c.cfg.Enabled {
		logger.Info("feishu: channel disabled, long connection skipped")
		return nil
	}
	if strings.TrimSpace(c.cfg.AppID) == "" || strings.TrimSpace(c.cfg.AppSecret) == "" {
		return ErrMissingCredentials
	}
	if c.starter == nil {
		return errors.New("feishu: starter not configured")
	}
	logger.Info("feishu: starting long connection")
	return c.starter.Start(ctx)
}

// ErrMissingCredentials 启用飞书 channel 但 app_id/app_secret 为空——
// 早失败,避免 SDK 内部抛模糊错误。
var ErrMissingCredentials = errors.New("feishu: app_id and app_secret are required when enabled")

// ===== 默认 starter:绑飞书 ws SDK + dispatcher =====

// larkWSStarter 包装 *larkws.Client,把 Start 适配到 Starter 接口。
type larkWSStarter struct {
	client *larkws.Client
}

// newLarkWSStarter 构造默认的飞书长连接启动器。SDK 内部用 dispatcher 路由事件。
// dispatcher 的 verificationToken / eventEncryptKey 在长连接模式下不强制(留空),
// SDK 通过 app credentials 验证。
func newLarkWSStarter(cfg Config, h *MessageHandler) *larkWSStarter {
	disp := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			return dispatchInbound(ctx, h, event)
		})

	// 把已知但当前不处理的"杂音"事件 dump 到日志,避免 SDK 反复打
	// ERROR `not found handler` 又看不到 payload。新事件类型继续走
	// 原路径报 not found,提示开发者扩 chatterEventTypes 白名单。
	registerUnhandledEventDumper(disp, chatterEventTypes)

	c := larkws.NewClient(cfg.AppID, cfg.AppSecret, larkws.WithEventHandler(disp))
	return &larkWSStarter{client: c}
}

// chatterEventTypes — 飞书会推送但我们当前不处理的事件类型清单。
// 这些事件被 registerUnhandledEventDumper 显式注册成 dump-and-ignore
// handler,payload 会写入 zap INFO 日志。
//
// 添加新条目的判断标准:
//  1. 飞书推送的事件我们没注册业务 handler,SDK 打 `not found handler` ERROR
//  2. 我们看完事件含义后,确认**不需要业务响应**(否则应该写业务 handler)
//  3. 但希望保留可见性,以便未来需要时直接查 payload 实现
//
// 不要把"想做但还没做"的事件加进来——那应该写 TODO + 实际 handler。
var chatterEventTypes = []string{
	// 用户打开机器人单聊会话(只是会话进入,不是消息):
	// 飞书在 v2026 之后默认推送,用于支持「机器人欢迎语」等场景。
	// 我们当前不做欢迎语,纯静默忽略。
	"im.chat.access_event.bot_p2p_chat_entered_v1",
}

// registerUnhandledEventDumper 把白名单事件类型注册成 dump-and-ignore handler。
// 每条事件 payload 会写到 zap INFO 日志 `feishu: unhandled event dump`,
// 便于排查飞书后台事件订阅配置(看日志就知道平台真的推了什么)。
//
// SDK API 限制:OnCustomizedEvent 只接受具体 eventType,没有通配符。所以
// 白名单是显式行为——任何**白名单外**的新事件类型仍会走 SDK 默认路径打
// ERROR `not found handler`,这正是我们想要的:新事件不会被无声吞掉。
func registerUnhandledEventDumper(disp *dispatcher.EventDispatcher, eventTypes []string) {
	for _, evType := range eventTypes {
		evType := evType // capture for closure
		disp.OnCustomizedEvent(evType, func(ctx context.Context, req *larkevent.EventReq) error {
			logger.InfoWithFields(
				"feishu: unhandled event dump",
				zap.String("event_type", evType),
				zap.String("payload", string(req.Body)),
			)
			return nil
		})
	}
}

// Start 启动长连接(SDK 内部循环阻塞)。
func (s *larkWSStarter) Start(ctx context.Context) error {
	return s.client.Start(ctx)
}

// dispatchInbound 把 SDK 的飞书消息事件解析为中性 InboundMessage,然后调 handler。
// 这层只做翻译,所有业务逻辑在 MessageHandler 内,完全可单测。
//
// 安全:解析失败、非 text 消息、缺字段都返回 nil(SDK 视为已处理,避免重投);
// 业务错误由 handler 内部处理,不上抛给 SDK 触发重连风暴。
func dispatchInbound(ctx context.Context, h *MessageHandler, event *larkim.P2MessageReceiveV1) error {
	if event == nil || event.Event == nil || event.Event.Message == nil || event.Event.Sender == nil {
		return nil
	}
	msg := event.Event.Message
	sender := event.Event.Sender
	if msg.MessageType == nil || *msg.MessageType != "text" {
		// v1.6 仅文本;非 text 消息静默忽略(后续可加友好提示)
		return nil
	}

	openID := derefString(sender.SenderId.OpenId)
	if openID == "" {
		return nil
	}

	// 文本消息 content 是 JSON 字符串 `{"text":"..."}`,需要解析提取
	rawContent := derefString(msg.Content)
	text := extractTextFromContent(rawContent)
	if text == "" {
		return nil
	}

	in := InboundMessage{
		OpenID:   openID,
		Text:     text,
		MsgID:    derefString(msg.MessageId),
		ChatType: derefString(msg.ChatType),
	}

	// recover 兜底:单条消息 panic 不应让长连接断
	defer func() {
		if r := recover(); r != nil {
			logger.ErrorWithFields("feishu: panic in handler", zap.Any("recover", r), zap.String("msg_id", in.MsgID))
		}
	}()
	if err := h.HandleInbound(ctx, in); err != nil {
		logger.ErrorWithFields("feishu: handler returned error", zap.String("msg_id", in.MsgID), zap.Error(err))
	}
	return nil
}

// extractTextFromContent 飞书 text 消息 content 是 JSON `{"text":"hello @机器人"}`,取 text 字段。
// 失败/空文本返回空串(让上层过滤)。
func extractTextFromContent(content string) string {
	if content == "" {
		return ""
	}
	var payload struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return ""
	}
	return payload.Text
}

func derefString(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
