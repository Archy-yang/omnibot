package search

// config.go — 联网搜索的用户级配置实体(32-联网搜索技术方案 §4.2)。
//
// 为什么是用户级:搜索服务按请求计费,多用户各自持 Key 各付各的;
// 且与 user_llm_configs 同一范式(设置里配、密钥加密落库、即时生效)。
//
// APIKey 存密文(crypto.Encrypt),明文只在 service 内存中出现。

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// 服务商与默认值。v1 只实现 deepseek;新增服务商 = 加一个 adapter,
// 不改表结构(provider 列已留)。
const (
	ProviderDeepSeek = "deepseek"

	DefaultBaseURL        = "https://api.deepseek.com"
	DefaultCount          = 5
	MinCount              = 1
	MaxCount              = 20 // 低于 API 上限 50,控 token 预算
	DefaultTimeoutSeconds = 20
	MinTimeoutSeconds     = 5
	MaxTimeoutSeconds     = 60
)

// Config 用户级联网搜索配置(表 user_search_configs)。
type Config struct {
	ID     int64 `gorm:"primaryKey;autoIncrement"`
	UserID int64 `gorm:"not null;uniqueIndex"`

	Provider string `gorm:"size:32;not null"`
	BaseURL  string `gorm:"size:256;not null"`
	// APIKey 密文(crypto.Encrypt);空 = 未配置。
	APIKey string `gorm:"size:512"`

	// Enabled 用户开关:关闭 = 助手不再使用联网搜索(工具仍可见,执行期返回未配置文案)。
	// 注意:此处刻意不打 gorm default 标签——bool 零值(false)会被 GORM 判为"未提供"
	// 而回落到 DB 默认值,导致"关闭后保存又变回开启"。开关一律由 service 显式赋值。
	Enabled bool `gorm:"not null"`

	DefaultCount   int  `gorm:"not null"`
	NeedContent    bool `gorm:"not null"`
	TimeoutSeconds int  `gorm:"not null"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName 表名。
func (Config) TableName() string { return "user_search_configs" }

// AllowedProvider 服务商是否在支持列表内。
func AllowedProvider(provider string) bool {
	return provider == ProviderDeepSeek
}

// Normalize 校验并补齐默认值(空值 → 默认;越界 → 中文错误)。
// 由 service 在落库前调用,保证库里不出现非法配置。
func (c *Config) Normalize() error {
	if c.Provider == "" {
		c.Provider = ProviderDeepSeek
	}
	if !AllowedProvider(c.Provider) {
		return fmt.Errorf("不支持的服务商:%s", c.Provider)
	}

	if c.BaseURL == "" {
		c.BaseURL = DefaultBaseURL
	}
	if err := validateBaseURL(c.BaseURL); err != nil {
		return err
	}

	if c.DefaultCount == 0 {
		c.DefaultCount = DefaultCount
	}
	if c.DefaultCount < MinCount || c.DefaultCount > MaxCount {
		return fmt.Errorf("每次条数应在 %d~%d 之间", MinCount, MaxCount)
	}

	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = DefaultTimeoutSeconds
	}
	if c.TimeoutSeconds < MinTimeoutSeconds || c.TimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("超时秒数应在 %d~%d 之间", MinTimeoutSeconds, MaxTimeoutSeconds)
	}
	return nil
}

// validateBaseURL 只校验 scheme 与主机名,不做内网拦截。
//
// 安全边界说明(勿"顺手修复"):这里允许 127.0.0.1 / 192.168.x.x 等内网地址。
// 该地址来自**用户自己的设置**,属于用户信任域(自建代理/内网网关是正常用法);
// web_read 的 validateURL 反的是另一类风险——URL 来自 LLM,可能被提示注入诱导
// 去读云元数据。两者威胁模型不同,不可套用同一策略。
func validateBaseURL(raw string) error {
	if len(raw) > 256 {
		return fmt.Errorf("服务地址过长(上限 256 字符)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("服务地址格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("服务地址必须以 http:// 或 https:// 开头")
	}
	if u.Hostname() == "" {
		return fmt.Errorf("服务地址缺少主机名")
	}
	if u.User != nil {
		return fmt.Errorf("服务地址不能包含用户名或密码,请把凭据填到 API Key")
	}
	return nil
}

// IsUsable 配置是否可用于发起搜索(启用 + 有密钥 + 地址合法)。
func (c *Config) IsUsable() bool {
	return c != nil && c.Enabled && c.APIKey != "" &&
		AllowedProvider(c.Provider) && c.BaseURL != ""
}

// CountOr 解析本次请求的结果条数:调用方显式指定且合法则用,否则回落到配置值。
func (c *Config) CountOr(requested int) int {
	if requested >= MinCount && requested <= MaxCount {
		return requested
	}
	if c.DefaultCount >= MinCount && c.DefaultCount <= MaxCount {
		return c.DefaultCount
	}
	return DefaultCount
}

// MaskKey 密钥脱敏回显:只露前 5 位与后 2 位;过短的 key 全掩。
func MaskKey(plain string) string {
	if plain == "" {
		return ""
	}
	if len(plain) <= 6 {
		return "***"
	}
	return plain[:5] + "..." + plain[len(plain)-2:]
}

// DefaultEndpoint 拼出搜索接口地址(base_url + /v1/tools/search)。
func (c *Config) DefaultEndpoint() string {
	base := strings.TrimRight(c.BaseURL, "/")
	if base == "" {
		base = DefaultBaseURL
	}
	return base + "/v1/tools/search"
}
