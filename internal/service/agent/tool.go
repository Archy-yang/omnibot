package agent

import (
	"context"
	"fmt"
)

// Tool 工具定义（参考 OpenAI Function Calling 格式）
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]interface{} // JSON Schema
	Execute     func(ctx context.Context, args map[string]interface{}) (string, error)
}

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
