package skill

import "time"

// MCP 鉴权方式(13-插件系统 M4)
const (
	AuthTypeNone   = "none"   // 无鉴权
	AuthTypeBearer = "bearer" // 静态 API Key(Authorization: Bearer)
	AuthTypeOAuth  = "oauth"  // OAuth 2.1 授权码+PKCE(远程托管 MCP 标准)
)

// MCPServer MCP 外部能力服务配置(M3 在线配置,系统级)。
// 以 DB 为单一事实源(config.yaml 仅作首次启动 seed);APIKey/密钥 AES 加密落库,
// 接口只回显掩码。密钥不入日志(安全红线)。
type MCPServer struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Name      string    `gorm:"uniqueIndex;size:64;not null"` // 自定义名,技能来源展示用
	BaseURL   string    `gorm:"size:512;not null"`            // Streamable HTTP 端点
	APIKey    string    `gorm:"size:1024"`                    // bearer: AES 密文;oauth: 空
	Enabled   bool      `gorm:"not null"`                     // false = 不连接、不同步、技能隐藏
	AuthType  string    `gorm:"size:16;not null;default:bearer"`

	// OAuth 2.1(M4):ClientID/Secret 可为空——空则尝试动态客户端注册(RFC 7591)。
	OAuthClientID     string    `gorm:"size:256"`
	OAuthClientSecret string    `gorm:"size:1024"` // AES 密文,可空(公共客户端)
	OAuthScopes       string    `gorm:"size:512"`  // 逗号分隔
	OAuthTokens       string    `gorm:"type:text"` // Token JSON 整体 AES 加密(enc: 前缀);空=未授权
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (MCPServer) TableName() string {
	return "mcp_servers"
}

// Authorized 是否已完成 OAuth 授权(存有 token)。
func (s *MCPServer) Authorized() bool {
	return s.AuthType == AuthTypeOAuth && s.OAuthTokens != ""
}

// ServerView 面向 API 的 server 视图(密钥只回显掩码/布尔)。
type ServerView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
	// HasAPIKey 是否配置了密钥(bearer 型;掩码展示用,不回显明文)
	HasAPIKey bool `json:"has_api_key"`
	// AuthType 鉴权方式:none/bearer/oauth
	AuthType string `json:"auth_type"`
	// Authorized OAuth 型是否已完成授权
	Authorized bool `json:"authorized"`
	// ToolCount 上次同步发现的工具数(-1=从未同步成功)
	ToolCount int `json:"tool_count"`
}
