package skill

import "time"

// 技能来源(13-Skill与MCP插件系统技术方案 §4)
const (
	SourceBuiltin = "builtin" // 内置技能(执行体在代码内,启动时 seed)
	SourceMCP     = "mcp"     // 外部 MCP server 提供(M2)
)

// Skill 技能定义 + 启停状态(单一事实源)。
// 定义(Name/Description/...)与执行体分离:定义落库可清单/启停,执行体两类——
// builtin 为代码内 builder,mcp 为远程调用(M2)。框架工具(request_input/delegate 等)
// 是 Agent 生存依赖,不入本表、不可停用(13-技术方案 §3 原则 2)。
type Skill struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	Name         string    `gorm:"uniqueIndex;size:64;not null"` // 工具名(ToolRegistry key)
	DisplayName  string    `gorm:"size:64"`                      // 面向用户的中文名
	Description  string    `gorm:"type:text"`                    // 给 LLM 的描述
	Source       string    `gorm:"size:16;not null;default:builtin"`
	Capabilities string    `gorm:"size:128"` // 逗号分隔,如 "research,web"
	ParamsSchema string    `gorm:"type:text"` // JSON Schema 字符串
	Enabled      bool      `gorm:"not null"` // 插入时显式赋值:builtin=true,mcp=false(勿加 default 标签——GORM 会省略零值,默认值会覆盖 false)
	// MainVisible 是否对主 Agent 可见。false = 子 Agent 专属技能(如抓取类 rss/web_read,
	// 方向 B:主 Agent 是管家,联网抓取必须 delegate 派活)。false 技能仍进子 Agent 全局池。
	MainVisible bool   `gorm:"not null;default:true"`
	MCPServer   string `gorm:"size:64;index"` // source=mcp 时所属 server 名(config.yaml 内的 name)
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

func (Skill) TableName() string {
	return "skills"
}

// BuiltinDef 内置技能定义(代码内,启动时 seed 进 skills 表)。
// 与 agent.Tool 的定义部分同构,由工厂函数直接提取,避免两处漂移。
type BuiltinDef struct {
	Name         string
	DisplayName  string
	Description  string
	Capabilities []string
	Parameters   map[string]interface{}
	MainVisible  bool
}

// MCPToolDef MCP server 发现的远端工具定义(source=mcp,启动同步时 upsert)。
type MCPToolDef struct {
	Name         string
	DisplayName  string
	Description  string
	MCPServer    string // 所属 server 名(config.yaml)
	ParamsSchema string // InputSchema JSON
	MainVisible  bool
	Enabled      bool // 仅插入时生效(默认停用);upsert 不覆盖用户启停
}
