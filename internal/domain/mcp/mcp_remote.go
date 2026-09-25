package mcp

import "encoding/json"

// MCPRemoteTool 远端 MCP 工具(ListTools 发现)。
// service 层的 MCPClient 窄接口以此与底层 SDK 类型解耦(传输库可替换)。
type MCPRemoteTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"` // JSON Schema 原文
}

// MCPToolCallResult 远端工具调用结果(只取文本内容;IsError=远端报错)。
type MCPToolCallResult struct {
	Text    string
	IsError bool
}
