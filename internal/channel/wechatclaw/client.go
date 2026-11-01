package wechatclaw

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// 协议常量(与官方插件 @tencent-weixin/openclaw-weixin@2.4.10 对齐)。
const (
	// DefaultBaseURL 微信 iLink AI 后端固定域名(登录成功后服务端可能下发 baseurl 覆盖)。
	DefaultBaseURL = "https://ilinkai.weixin.qq.com"
	// DefaultAppID 官方插件 package.json 里的 ilink_appid。
	DefaultAppID = "bot"
	// DefaultBotType 官方插件 get_bot_qrcode 使用的 bot_type。
	DefaultBotType = "3"
	// DefaultClientVersion iLink-App-ClientVersion: major<<16 | minor<<8 | patch。
	// 2.4.10 → 0x0002040A = 132106;服务端按版本做兼容,跟随官方最新稳定版。
	DefaultClientVersion = 132106
	// CompatChannelVersion base_info.channel_version,与官方插件版本保持一致。
	CompatChannelVersion = "2.4.10"
	// DefaultBotAgent base_info.bot_agent(UA 风格,仅观测用)。
	DefaultBotAgent = "OmniBot/2.8.0"

	// DefaultLongPollTimeout getupdates / get_qrcode_status 的长轮询超时。
	DefaultLongPollTimeout = 35 * time.Second
	// DefaultRequestTimeout 普通请求超时。
	DefaultRequestTimeout = 15 * time.Second
)

// ErrSessionExpired 会话失效(errcode/ret = -14):token 需要重新扫码。
var ErrSessionExpired = errors.New("wechatclaw: session expired, re-login required")

// ClientOptions 客户端配置。零值字段走默认值。
type ClientOptions struct {
	BaseURL       string
	Token         string
	AppID         string
	ClientVersion int
	// LongPollTimeout getupdates 等长轮询的单次超时。
	LongPollTimeout time.Duration
	// RequestTimeout 普通请求超时。
	RequestTimeout time.Duration
	// ChannelVersion / BotAgent 写进 base_info。
	ChannelVersion string
	BotAgent       string
	// HTTPClient 自定义 http.Client(测试/代理用)。
	HTTPClient *http.Client
	// UINGenerator 生成 X-WECHAT-UIN 的十进制随机数来源(测试固定值用)。
	UINGenerator func() string
}

// Client 微信 iLink 协议客户端。并发安全(只读配置 + 每次请求独立构造)。
type Client struct {
	baseURL        string
	token          string
	appID          string
	clientVersion  int
	longPollout    time.Duration
	requestTimeout time.Duration
	channelVersion string
	botAgent       string
	httpClient     *http.Client
	uinGenerator   func() string
}

// NewClient 创建协议客户端。
func NewClient(opts ClientOptions) *Client {
	c := &Client{
		baseURL:        strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/"),
		token:          opts.Token,
		appID:          opts.AppID,
		clientVersion:  opts.ClientVersion,
		longPollout:    opts.LongPollTimeout,
		requestTimeout: opts.RequestTimeout,
		channelVersion: opts.ChannelVersion,
		botAgent:       opts.BotAgent,
		httpClient:     opts.HTTPClient,
		uinGenerator:   opts.UINGenerator,
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.appID == "" {
		c.appID = DefaultAppID
	}
	if c.clientVersion == 0 {
		c.clientVersion = DefaultClientVersion
	}
	if c.longPollout <= 0 {
		c.longPollout = DefaultLongPollTimeout
	}
	if c.requestTimeout <= 0 {
		c.requestTimeout = DefaultRequestTimeout
	}
	if c.channelVersion == "" {
		c.channelVersion = CompatChannelVersion
	}
	if c.botAgent == "" {
		c.botAgent = DefaultBotAgent
	}
	if c.httpClient == nil {
		c.httpClient = &http.Client{}
	}
	if c.uinGenerator == nil {
		c.uinGenerator = randomUIN
	}
	return c
}

// Token 返回当前 token(日志/诊断用,调用方负责脱敏)。
func (c *Client) Token() string { return c.token }

// BaseURL 返回当前基址。
func (c *Client) BaseURL() string { return c.baseURL }

// randomUIN 随机 uint32 的十进制表示(与官方插件一致)。
func randomUIN() string {
	return strconv.FormatUint(uint64(rand.Uint32()), 10) //#nosec G404 -- 非加密用途,协议只要求随机
}

// buildCommonHeaders 两类请求共用的头。
func (c *Client) buildCommonHeaders() http.Header {
	h := http.Header{}
	h.Set("iLink-App-Id", c.appID)
	h.Set("iLink-App-ClientVersion", strconv.Itoa(c.clientVersion))
	return h
}

// buildHeaders POST 头:带鉴权 + 随机 UIN。
func (c *Client) buildHeaders() http.Header {
	h := c.buildCommonHeaders()
	h.Set("Content-Type", "application/json")
	h.Set("AuthorizationType", "ilink_bot_token")
	h.Set("X-WECHAT-UIN", base64.StdEncoding.EncodeToString([]byte(c.uinGenerator())))
	if token := strings.TrimSpace(c.token); token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	return h
}

// baseInfo 构造 base_info。
func (c *Client) baseInfo() baseInfo {
	return baseInfo{ChannelVersion: c.channelVersion, BotAgent: c.botAgent}
}

// doJSON 发请求并解析 JSON 响应。out 为 nil 时忽略响应体。
func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("wechatclaw: marshal request: %w", err)
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("wechatclaw: build request: %w", err)
	}
	if method == http.MethodPost {
		for k, vs := range c.buildHeaders() {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
	} else {
		req.Header = c.buildCommonHeaders()
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("wechatclaw: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("wechatclaw: read response: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("wechatclaw: %s %s: status %d", method, path, resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("wechatclaw: decode response: %w", err)
	}
	return nil
}

// GetBotQRCode 申请登录二维码。
func (c *Client) GetBotQRCode(ctx context.Context) (*QRCode, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("bot_type", DefaultBotType)
	var out QRCode
	if err := c.doJSON(ctx, http.MethodGet, "/ilink/bot/get_bot_qrcode?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Ret != 0 {
		return nil, fmt.Errorf("wechatclaw: get_bot_qrcode ret=%d errmsg=%s", out.Ret, out.ErrMsg)
	}
	return &out, nil
}

// GetQRCodeStatus 长轮询扫码状态。verifyCode 非空时带上(手机端显示数字时用)。
func (c *Client) GetQRCodeStatus(ctx context.Context, qrcode, verifyCode string) (*QRStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, c.longPollout)
	defer cancel()

	q := url.Values{}
	q.Set("qrcode", qrcode)
	if verifyCode != "" {
		q.Set("verify_code", verifyCode)
	}
	var out QRStatus
	if err := c.doJSON(ctx, http.MethodGet, "/ilink/bot/get_qrcode_status?"+q.Encode(), nil, &out); err != nil {
		return nil, err
	}
	if out.Ret != 0 && out.Ret != -14 {
		return nil, fmt.Errorf("wechatclaw: get_qrcode_status ret=%d errmsg=%s", out.Ret, out.ErrMsg)
	}
	return &out, nil
}

// GetUpdates 长轮询拉取消息。buf 为上次返回的 get_updates_buf 游标(首次传空串)。
// 客户端超时视为正常(返回空的 Updates),由调用方继续下一轮。
func (c *Client) GetUpdates(ctx context.Context, buf string) (*Updates, error) {
	ctx, cancel := context.WithTimeout(ctx, c.longPollout)
	defer cancel()

	body := map[string]any{
		"get_updates_buf": buf,
		"base_info":       c.baseInfo(),
	}
	var out Updates
	err := c.doJSON(ctx, http.MethodPost, "/ilink/bot/getupdates", body, &out)
	if err != nil {
		// 长轮询超时是正常控制流:返回空结果 + 原游标
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return &Updates{Ret: 0, Msgs: nil, GetUpdatesBuf: buf}, nil
		}
		return nil, err
	}
	if out.ErrCode == -14 || out.Ret == -14 {
		return nil, ErrSessionExpired
	}
	if out.Ret != 0 || out.ErrCode != 0 {
		return nil, fmt.Errorf("wechatclaw: getupdates ret=%d errcode=%d errmsg=%s", out.Ret, out.ErrCode, out.ErrMsg)
	}
	return &out, nil
}

// SendText 发送单条文本消息,返回服务端消息 id。
// contextToken 必须来自该对端的入站消息,否则服务端会拒绝或串会话。
func (c *Client) SendText(ctx context.Context, toUserID, text, contextToken, clientID string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	req := sendMessageReq{
		Msg: sendMsg{
			ToUserID:     toUserID,
			ClientID:     clientID,
			MessageType:  MessageTypeBot,
			MessageState: MessageStateFinish,
			ItemList:     []MessageItem{{Type: ItemTypeText, TextItem: &TextItem{Text: text}}},
			ContextToken: contextToken,
		},
		BaseInfo: c.baseInfo(),
	}
	var out SendMessageResp
	if err := c.doJSON(ctx, http.MethodPost, "/ilink/bot/sendmessage", req, &out); err != nil {
		return "", err
	}
	if out.Ret != 0 {
		return "", fmt.Errorf("wechatclaw: sendmessage ret=%d errmsg=%s", out.Ret, out.ErrMsg)
	}
	return out.MessageID.String(), nil
}

// NotifyStart 通知服务端「通道客户端已启动」。
func (c *Client) NotifyStart(ctx context.Context) error {
	return c.notify(ctx, "/ilink/bot/msg/notifystart")
}

// NotifyStop 通知服务端「通道客户端已停止」。
func (c *Client) NotifyStop(ctx context.Context) error {
	return c.notify(ctx, "/ilink/bot/msg/notifystop")
}

func (c *Client) notify(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	var out CloseResp
	if err := c.doJSON(ctx, http.MethodPost, path, map[string]any{"base_info": c.baseInfo()}, &out); err != nil {
		return err
	}
	if out.Ret != 0 {
		return fmt.Errorf("wechatclaw: %s ret=%d errmsg=%s", path, out.Ret, out.ErrMsg)
	}
	return nil
}
