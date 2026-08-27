package agent

import (
	"context"
	"fmt"
)

// Tool 工具定义（参考 OpenAI Function Calling 格式）
type Tool struct {
	Name        string
	Description string
	// DisplayLabel 是面向用户的中文友好文案，用于流式 Agent 展示「正在调用 xxx」状态条。
	// 留空时 UI 端会回落到 Name，避免出现内部英文工具名直接外露的尴尬。
	DisplayLabel string
	// Capabilities 能力标签列表。子 Agent 工具可见性由「本工具能力 ∩ 配置允许集」决定(仿 DSH ToolProviderResult)，
	// 取代旧的角色卡固定 Tools 列表。详见 08-后台Agent任务框架 工具裁剪能力化。
	Capabilities []string
	Parameters   map[string]interface{} // JSON Schema
	Execute      func(ctx context.Context, args map[string]interface{}) (string, error)
}

// 子 Agent 工具能力标签常量(capability 白名单的取值域)。
// 给工具打标:一个工具可具多个能力;config 的 allowed_capabilities 命中的能力所覆盖的工具才对子 Agent 可见。
const (
	CapBasic       = "basic"       // 基础/通用(get_current_time, calculator)
	CapMemory      = "memory"      // 记忆检索(search_memories, search_history)
	CapResearch    = "research"    // 研究/检索类(rss_reader, web_read 及记忆检索)
	CapWeb         = "web"         // 联网抓取(web_read, rss_reader)
	CapIngest      = "ingest"      // 信息摄入汇总(rss_reader)
	CapInteractive = "interactive" // 与用户/主 Agent 交互(request_input 强制基线)
	// CapDelegateOwner 主 Agent 专用(不参与子 Agent capability 白名单):delegate/query/update/cancel_task。
	// 它们注册在 main 专用 agentToolRegistry 而非 globalToolRegistry,天然不对子 Agent 可见,无需打标。
)

// ToolCall 表示 LLM 发起的一次工具调用
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// ToolRegistry 工具注册中心
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry 创建工具注册中心
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 注册工具，重名返回错误
func (r *ToolRegistry) Register(tool Tool) error {
	if _, exists := r.tools[tool.Name]; exists {
		return fmt.Errorf("tool %q already registered", tool.Name)
	}
	r.tools[tool.Name] = tool
	return nil
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// ListAll 列出所有已注册工具
func (r *ToolRegistry) ListAll() []Tool {
	result := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}

// ToOpenAITools 转为 OpenAI tools 格式
func (r *ToolRegistry) ToOpenAITools() []map[string]interface{} {
	tools := make([]map[string]interface{}, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			},
		})
	}
	return tools
}
