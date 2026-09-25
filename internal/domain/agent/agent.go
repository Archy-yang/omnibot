package agent

import (
	"time"
)

// AgentCodeMain 内置主助理 agent 的稳定标识(目前系统唯一对话方)。
// agents 表是全场景预留(16-架构迭代路线图 §5.2):将来出现第二个 Agent 时,
// conversation 按 (user_id, agent_id) 切分对话室,子 Agent 仍走 AgentTask 不拥有 conversation。
const AgentCodeMain = "main"

// Agent 执行体登记簿:拥有 conversation 的对话方(主 Agent),最小化建模。
type Agent struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	Code      string    `gorm:"size:32;uniqueIndex;not null"` // 稳定标识:"main"
	Name      string    `gorm:"size:64;not null"`             // 展示名
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time
}

// TableName 指定表名
func (Agent) TableName() string {
	return "agents"
}
