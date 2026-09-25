package tool

import "time"

// Tool 工具(function-call 可执行单元)定义 + 启停状态(单一事实源)。
// 定义(Name/Description/...)与执行体分离:定义落库可清单/启停,执行体为代码内 builder。
// 框架工具(request_input/delegate/mcp_call 等)是 Agent 生存依赖,不入本表、不可停用。
// MCP 连接器提供的工具不入本表(目录在内存缓存,见 13-技术方案);Skill 概念留白给未来能力包。
type Tool struct {
	ID          int64  `gorm:"primaryKey;autoIncrement"`
	Name        string `gorm:"uniqueIndex;size:64;not null"` // 工具名(ToolRegistry key)
	DisplayName string `gorm:"size:64"`                      // 面向用户的中文名
	Description string `gorm:"type:text"`                    // 给 LLM 的描述
	// Capabilities 逗号分隔能力标签,如 "research,web"(工具可见性由「能力标签 ∩ 全局配置」决定)。
	Capabilities string `gorm:"size:128"`
	ParamsSchema string `gorm:"type:text"` // JSON Schema 字符串
	Enabled      bool   `gorm:"not null"`  // 插入时显式赋值:builtin=true(勿加 default 标签——GORM 会省略零值,默认值会覆盖 false)
	// MainVisible 是否对主 Agent 可见。false = 子 Agent 专属工具(如抓取类 rss/web_read,
	// 方向 B:主 Agent 是管家,联网抓取必须 delegate 派活)。false 工具仍进子 Agent 全局池。
	// 勿加 default 标签(同 Enabled):GORM 对零值+default 字段 INSERT 时省略该列,
	// ON CONFLICT 的 excluded 会取 DB 默认值 true,false 永远写不进去。
	MainVisible bool `gorm:"not null"`
	CreatedAt   time.Time `gorm:"not null"`
	UpdatedAt   time.Time `gorm:"not null"`
}

func (Tool) TableName() string {
	return "tools"
}

// ToolDef 内置工具定义(代码内,启动时 seed 进 tools 表)。
// 与 agent.Tool 的定义部分同构,由工厂函数直接提取,避免两处漂移。
type ToolDef struct {
	Name         string
	DisplayName  string
	Description  string
	Capabilities []string
	Parameters   map[string]interface{}
	MainVisible  bool
}
