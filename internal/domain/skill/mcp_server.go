package skill

import "time"

// MCPServer MCP 外部能力服务配置(M3 在线配置,系统级)。
// 以 DB 为单一事实源(config.yaml 仅作首次启动 seed);APIKey AES 加密落库,
// 接口只回显掩码。密钥不入日志(安全红线)。
type MCPServer struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Name      string    `gorm:"uniqueIndex;size:64;not null"` // 自定义名,技能来源展示用
	BaseURL   string    `gorm:"size:512;not null"`            // Streamable HTTP 端点
	APIKey    string    `gorm:"size:1024"`                    // AES 密文(base64),空=无鉴权
	Enabled   bool      `gorm:"not null"`                     // false = 不连接、不同步、技能隐藏
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (MCPServer) TableName() string {
	return "mcp_servers"
}

// ServerView 面向 API 的 server 视图(APIKey 只回显掩码)。
type ServerView struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Enabled bool   `json:"enabled"`
	// HasAPIKey 是否配置了密钥(掩码展示用,不回显明文)
	HasAPIKey bool `json:"has_api_key"`
	// ToolCount 上次同步发现的工具数(-1=从未同步成功)
	ToolCount int `json:"tool_count"`
}
