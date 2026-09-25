package mcp

import "time"

// MCP 鉴权方式(13-插件系统 M4)
const (
	AuthTypeNone   = "none"   // 无鉴权
	AuthTypeBearer = "bearer" // 静态 API Key(Authorization: Bearer)
	AuthTypeOAuth  = "oauth"  // OAuth 2.1 授权码+PKCE(远程托管 MCP 标准)
	AuthTypeQuery  = "query"  // URL 参数鉴权:key=<APIKey>(高德等国内平台惯例)
)

// MCP 传输协议(Phase 3.5)。空值 = streamable + 自动回退,不单设 auto 常量。
const (
	TransportStreamable = "streamable" // Streamable HTTP(2025-03 协议,默认)
	TransportSSE        = "sse"        // HTTP+SSE(2024-11 协议,高德等平台仍在用)
)

// MCPServer MCP 外部能力服务配置(M3 在线配置,系统级)。
// 以 DB 为单一事实源(config.yaml 仅作首次启动 seed);APIKey/密钥 AES 加密落库,
// 接口只回显掩码。密钥不入日志(安全红线)。
type MCPServer struct {
	ID       int64  `gorm:"primaryKey;autoIncrement"`
	Name     string `gorm:"uniqueIndex;size:64;not null"` // 自定义名,连接器展示名
	// Description 一句话概述该连接器提供的能力(选填;连接器 UI 展示 + 拼入工具描述
	// 向量化文本,改善语义匹配——工具描述干瘪时 server 级上下文补位)。
	Description string `gorm:"size:256"`
	BaseURL     string `gorm:"size:512;not null"` // Streamable HTTP 端点
	APIKey      string `gorm:"size:1024"`         // bearer: AES 密文;oauth: 空
	Enabled  bool   `gorm:"not null"`                     // false = 不连接、不同步、工具不可见
	AuthType string `gorm:"size:16;not null;default:bearer"`

	// Transport MCP 传输协议:"" = streamable(存量兼容,同步失败自动回退 SSE 重试一次)
	// | "streamable" | "sse"。高德等平台官方端点是 SSE(/sse?key=key):
	// streamable 客户端连 SSE 端点会报 "response should contain RPC id"。
	Transport string `gorm:"size:16"`

	// UserID 归属用户(MCP 按人隔离):NULL = 共享服务(所有用户可调用);
	// 非 NULL = 私有服务(仅本人可调用/可见/可管理)。私有与共享同名工具时私有遮蔽共享。
	UserID     *int64    `gorm:"index"`

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
	// Description 能力概述(选填;空=未填)
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	// HasAPIKey 是否配置了密钥(bearer 型;掩码展示用,不回显明文)
	HasAPIKey bool `json:"has_api_key"`
	// AuthType 鉴权方式:none/bearer/oauth
	AuthType string `json:"auth_type"`
	// Authorized OAuth 型是否已完成授权
	Authorized bool `json:"authorized"`
	// Transport 传输协议:streamable(空值同)/sse
	Transport string `json:"transport"`
	// ToolCount 上次同步发现的工具数(-1=从未同步成功)
	ToolCount int `json:"tool_count"`
	// UserID 归属(NULL=共享)
	UserID *int64 `json:"user_id"`
	// Tools 目录中的工具能力(连接器 UI 折叠展示用;未同步为空)
	Tools []MCPToolCard `json:"tools,omitempty"`
}

// MCPToolCard 连接器工具卡片(UI 展示)。
type MCPToolCard struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}
