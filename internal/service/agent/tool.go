package agent

// tool.go — Agent 运行时的工具类型入口。
// 2026-09-25 起 Tool/ToolCall/ToolRegistry/能力常量的定义下沉到中立包
// internal/pkg/toolcore(DeepSeek 架构审查 §7.3:解除 tool/mcp/tools 三向依赖
// service/agent 的分层倒挂)。本文件以类型别名保持既有引用(agent 包内及外部
// 依赖 agent.Tool 的调用方)零改动兼容;新代码建议直接 import toolcore。

import (
	"omnibot/internal/pkg/toolcore"
)

// Tool 工具定义(OpenAI Function Calling 格式)。
type Tool = toolcore.Tool

// ToolCall 表示 LLM 发起的一次工具调用。
type ToolCall = toolcore.ToolCall

// ToolRegistry 工具注册中心(并发安全,见 toolcore 文档)。
type ToolRegistry = toolcore.ToolRegistry

// NewToolRegistry 创建工具注册中心。
func NewToolRegistry() *ToolRegistry {
	return toolcore.NewToolRegistry()
}

// 子 Agent 工具能力标签常量(capability 白名单的取值域),定义见 toolcore。
const (
	CapBasic       = toolcore.CapBasic
	CapMemory      = toolcore.CapMemory
	CapResearch    = toolcore.CapResearch
	CapWeb         = toolcore.CapWeb
	CapIngest      = toolcore.CapIngest
	CapInteractive = toolcore.CapInteractive
)
