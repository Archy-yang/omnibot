// Package toolcore 工具域中立契约:工具类型、能力常量、工具注册中心与工具 ctx 助手。
//
// 2026-09-25 自 service/agent 下沉(DeepSeek 架构审查 §7.3):此前 Tool 接口定义在
// agent 运行时包内,导致 service/tool、service/mcp、agent/tools 三方向上依赖
// service/agent(分层倒挂,Tool 接口一改波及 3 包)。下沉后:
//
//	agent 运行时(tool.go)       → 类型别名引用本包(向后兼容)
//	service/tool / service/mcp  → 直接 import 本包,不再依赖 agent
//	agent/tools(领域工具)       → 直接 import 本包,不再反向 import 父包
//
// 本包只含契约与纯数据结构,不得 import 任何 service 层包。
package toolcore

import (
	"context"
	"fmt"
	"sync"
)

// Tool 工具定义(参考 OpenAI Function Calling 格式)。
type Tool struct {
	Name        string
	Description string
	// DisplayLabel 是面向用户的中文友好文案,用于流式 Agent 展示「正在调用 xxx」状态条。
	// 留空时 UI 端会回落到 Name,避免出现内部英文工具名直接外露的尴尬。
	DisplayLabel string
	// Capabilities 能力标签列表。子 Agent 工具可见性由「本工具能力 ∩ 配置允许集」决定(仿 DSH ToolProviderResult),
	// 取代旧的角色卡固定 Tools 列表。详见 08-后台Agent任务框架 工具裁剪能力化。
	Capabilities []string
	Parameters   map[string]interface{} // JSON Schema
	Execute      func(ctx context.Context, args map[string]interface{}) (string, error)
}

// 能力标签常量(capability 白名单的取值域)。
// 给工具打标:一个工具可具多个能力;config 的 allowed_capabilities 命中的能力所覆盖的工具才对子 Agent 可见。
const (
	CapBasic       = "basic"       // 基础/通用(get_current_time, calculator)
	CapMemory      = "memory"      // 记忆检索(search_memories)
	CapResearch    = "research"    // 研究/检索类(rss_reader, web_read 及记忆检索)
	CapWeb         = "web"         // 联网抓取(web_read, rss_reader)
	CapIngest      = "ingest"      // 信息摄入汇总(rss_reader)
	CapInteractive = "interactive" // 与用户/主 Agent 交互(request_input 强制基线)
	// CapDelegateOwner 主 Agent 专用(不参与子 Agent capability 白名单):delegate/query/update/cancel_task。
	// 它们注册在 main 专用 agentToolRegistry 而非 globalToolRegistry,天然不对子 Agent 可见,无需打标。
)

// ToolCall 表示 LLM 发起的一次工具调用。
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]interface{}
}

// ToolRegistry 工具注册中心。
// 并发安全:技能启停会在运行中原位增删工具(skill 服务 ApplyTo),而 Agent 执行链
// 在读 registry,故读写都走 RWMutex(13-Skill与MCP插件系统技术方案 §5.2)。
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewToolRegistry 创建工具注册中心。
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 注册工具,重名返回错误。
func (r *ToolRegistry) Register(tool Tool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; exists {
		return fmt.Errorf("tool %q already registered", tool.Name)
	}
	r.tools[tool.Name] = tool
	return nil
}

// Remove 注销工具(技能启停的幂等重建用)。不存在时静默返回。
func (r *ToolRegistry) Remove(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
}

// Get 获取工具。
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

// ListAll 列出所有已注册工具。
func (r *ToolRegistry) ListAll() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		result = append(result, tool)
	}
	return result
}

// ToOpenAITools 转为 OpenAI tools 格式。
func (r *ToolRegistry) ToOpenAITools() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()
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

// ---- 工具 ctx 助手(原 agent 包 ctx 注入/读取,工具实现层统一由此取) ----

// ContextKey ctx 键类型(工具域专用,字符串值仅调试可读)。
type ContextKey string

const (
	// UserIDKey 工具 ctx 中用户 id 的键。
	UserIDKey ContextKey = "agent_user_id"
	// TaskIDKey 子 Agent 运行时的 taskID(供 request_input 工具用)。
	TaskIDKey ContextKey = "agent_task_id"
	// SourceKey 任务来源渠道(web/feishu),供 delegate 记录到 task。
	SourceKey ContextKey = "agent_source"
	// NotifyTargetKey 主动推送目标(feishu=open_id),供 delegate 记录。
	NotifyTargetKey ContextKey = "agent_notify_target"
	// mcpSearchKey 每回合 mcp_search 调用计数键。
	mcpSearchKey ContextKey = "agent_mcp_search"
)

// WithUserID 注入用户 id(handler 层调)。
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// UserIDFromContext 读取用户 id(未注入返回 0)。
func UserIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(UserIDKey).(int64); ok {
		return id
	}
	return 0
}

// WithTaskID 注入 taskID(子 Agent runner 启动时调,供 request_input 工具取)。
func WithTaskID(ctx context.Context, taskID int64) context.Context {
	return context.WithValue(ctx, TaskIDKey, taskID)
}

// TaskIDFromContext 读取 taskID(未注入返回 0)。
func TaskIDFromContext(ctx context.Context) int64 {
	if id, ok := ctx.Value(TaskIDKey).(int64); ok {
		return id
	}
	return 0
}

// WithSource 注入来源渠道(web/feishu handler 调,供 delegate 记录到 task.Source)。
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, SourceKey, source)
}

// SourceFromContext 读取来源渠道(未注入返回空串)。
func SourceFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(SourceKey).(string); ok {
		return s
	}
	return ""
}

// WithNotifyTarget 注入主动推送目标(feishu handler 注入 open_id,供 delegate 记录)。
func WithNotifyTarget(ctx context.Context, target string) context.Context {
	return context.WithValue(ctx, NotifyTargetKey, target)
}

// NotifyTargetFromContext 读取推送目标(未注入返回空串)。
func NotifyTargetFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(NotifyTargetKey).(string); ok {
		return s
	}
	return ""
}

// WithMCPSearchCounter 初始化本回合的 MCP 搜索计数器(ReActAgent 每轮执行开始注入)。
func WithMCPSearchCounter(ctx context.Context) context.Context {
	return context.WithValue(ctx, mcpSearchKey, new(int32))
}

// MCPSearchCounter 取计数器(未注入返回 nil,工具侧跳过限流)。
func MCPSearchCounter(ctx context.Context) *int32 {
	if c, ok := ctx.Value(mcpSearchKey).(*int32); ok {
		return c
	}
	return nil
}
