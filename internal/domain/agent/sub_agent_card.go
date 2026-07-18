package agent

import "time"

// SubAgentCard 子 Agent 能力声明(A2A Agent Card 的进程内子集)。
//
// 主 Agent 的 delegate 工具描述会动态列出所有已注册 SubAgentCard 的 Name+Description,
// 主 Agent LLM 据此决定是否派活。派活时 PromptTemplate 填入 {goal} 生成子 Agent system prompt。
//
// 子 Agent 用独立 ToolRegistry(Tools 指定的工具集),与主 Agent 工具集可不同。
// 第一版 MaxSteps/Timeout 固定,不做 LLM 自判停止。
type SubAgentCard struct {
	Type           string        // 子 Agent 类型标识,如 "researcher"(对应 AgentTask.SubAgentType)
	Name           string        // 面向主 Agent 的名称(写进 delegate 工具描述)
	Description    string        // 能力描述,主 Agent LLM 据此决定是否派活
	PromptTemplate string        // 委托指令模板:派活时填入 {goal} 生成子 Agent system prompt
	Tools          []string      // 子 Agent 可用工具名列表(从全局工具池选)
	MaxSteps       int           // 停止条件:最大步数
	Timeout        time.Duration // 停止条件:超时
}
