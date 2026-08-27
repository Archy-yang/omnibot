package agent

import "time"

// SubAgentCard 子 Agent 能力声明(A2A Agent Card 的进程内子集)。
//
// 主 Agent 的 delegate 工具描述会动态列出所有已注册 SubAgentCard 的 Name+Description,
// 主 Agent LLM 据此决定是否派活。派活时 PromptTemplate 填入 {goal} 生成子 Agent system prompt。
//
// 子 Agent 用独立 ToolRegistry,但**可见工具集不再由本卡固定列表决定**,而由「工具自身能力标签 ∩ 配置
// agent.sub_agent.allowed_capabilities」算出(仿 DSH ToolProviderResult,见 tool_provider.go)。
// 分类轴从"角色"下沉到"工具能力",子 Agent 是通用执行器而非固定角色。
// 第一版 MaxSteps/Timeout 固定,不做 LLM 自判停止。
type SubAgentCard struct {
	Type           string        // 子 Agent 类型标识,如 "researcher"(对应 AgentTask.SubAgentType)
	Name           string        // 面向主 Agent 的名称(写进 delegate 工具描述)
	Description    string        // 能力描述,主 Agent LLM 据此决定是否派活
	PromptTemplate string        // 委托指令模板:派活时填入 {goal} 生成子 Agent system prompt
	MaxSteps       int           // 停止条件:最大步数
	Timeout        time.Duration // 停止条件:超时
}
