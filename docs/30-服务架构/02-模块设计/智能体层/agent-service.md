# Agent 服务

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.5+ |
| 最后更新 | 2026-06-15 |
| 状态 | ✅ 已实现 |

---

## 1. 这个服务解决什么问题？

Agent 服务让 OmniBot 从「单轮文本生成」升级为「可调用工具的多步智能体」。

核心能力：

1. LLM 可以通过 OpenAI Function Calling 协议调用工具
2. Agent 执行 ReAct 循环：LLM 推理 → 工具调用 → 工具结果回传 → 继续推理
3. Web SSE 可以展示 Agent 的工具调用过程
4. 工具注册中心统一管理内置工具

---

## 2. 模块位置

```
Web Handler
    ↓
Agent Service
    ├─ ReActAgent
    ├─ ToolRegistry
    ├─ OpenAILLMClient adapter
    └─ Built-in Tools
        ├─ get_current_time
        ├─ calculator
        ├─ search_memories
        └─ search_history
```

Agent 层位于 API Handler 与 LLM Client 之间。现有 `LLMProvider` 接口保持不变，Agent 专用的 `OpenAILLMClient` 直接构造 OpenAI-compatible `/chat/completions` 请求并携带 `tools` 参数。

---

## 3. 核心接口

### Tool

```go
type Tool struct {
    Name         string
    Description  string
    DisplayLabel string  // v1.5.2 起：面向用户的中文友好文案，留空时回落到 Name
    Parameters   map[string]interface{}
    Execute      func(ctx context.Context, args map[string]interface{}) (string, error)
}
```

### LLMClient（同步）

```go
type LLMClient interface {
    ChatCompletion(ctx, messages, tools) (content string, toolCalls []map[string]interface{}, err error)
}
```

### StreamingLLMClient（流式，v1.5.2 新增）

```go
type StreamingLLMClient interface {
    ChatCompletionStream(ctx, messages, tools) (<-chan LLMStreamChunk, error)
}
```

`LLMStreamChunk` 是 SSE 单行解析后的原始增量单元，包含 `ContentDelta` /
`ToolCallDelta` / `FinishReason` / `Done` / `Error` 字段。同一个
`OpenAILLMClient` 同时实现 `LLMClient` 和 `StreamingLLMClient`。

### ReActAgent

```go
type ReActAgent struct {
    llmClient       LLMClient
    streamingClient StreamingLLMClient  // 流式路径
    toolRegistry    *ToolRegistry
    maxSteps        int
    timeout         time.Duration
    systemPrompt    string
}
```

两个执行入口：

- `Run(ctx, conversation) (*AgentResult, error)` —— 同步执行整个 ReAct 循环，
  返回完整结果。供非流式场景（API 集成、未来异步任务等）使用。
- `RunStream(ctx, conversation) (<-chan AgentEvent, error)` —— 流式执行，按时序
  emit `AgentEvent`（Token / ToolCall / ToolResult / Done / Error）。这是
  Web 端 `/messages/agent/stream` 的底层实现。

### AgentService

```go
func (s *AgentService) Run(ctx, userID, conversation, customLLMClient...) (*AgentResult, error)
func (s *AgentService) RunStream(ctx, userID, conversation, customStreamClient...) (<-chan AgentEvent, error)
```

---

## 4. ReAct 循环

```
用户消息
  ↓
构造上下文 + system prompt
  ↓
LLM ChatCompletion(tools)
  ├─ 无 tool_calls → 返回最终回复
  └─ 有 tool_calls
       ↓
     执行工具
       ↓
     工具结果作为 tool message 回传
       ↓
     继续下一轮 LLM 调用
```

终止条件：

- LLM 返回无 `tool_calls` 的最终文本
- 达到最大步数（默认 10）
- 超时（默认 120 秒）
- 上下文取消

---

## 5. Web API

新增端点：

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/chat/messages/agent` | Agent 同步对话 |
| POST | `/api/v1/chat/messages/agent/stream` | Agent SSE 流式对话（v1.5.2 起为真流式） |

### SSE 协议（v1.5.2）

简单提问（无工具调用）—— 体验等同普通流式：

```text
event: token
data: {"content":"你"}

event: token
data: {"content":"好"}

data: [DONE]
```

工具调用场景 —— 工具调用立即推送状态，工具结果随后推送（供前端展开看详情），
再继续 token 流：

```text
event: tool_call
data: {"tool":"get_current_time","label":"查询了当前时间"}

event: tool_result
data: {"tool":"get_current_time","result":"10:30"}

event: token
data: {"content":"现在是 "}

event: token
data: {"content":"10:30"}

data: [DONE]
```

错误：

```text
event: error
data: {"error":"streaming client not configured"}
```

> 旧 `event: agent_step` 协议（v1.5.0 ~ v1.5.1）已下线，前端不再监听，后端不再产出。
> `event: tool_result`（v1.5.3 起）携带工具结果，供前端「点击思考条展开看详情」。
> 执行失败的结果已脱敏为「工具执行失败」，不透传原始 error（IP / 连接错误 / 堆栈），
> 见 `sanitizeToolResult`（`internal/api/web/handler.go`）。tool_result 不计入落库内容。
> 事件严格按 LLM 真实时序推送，前端据此交错渲染「文本 → 思考 → 文本」。

---

## 6. 安全约束

- v1.5 仅注册系统内置工具，不支持用户自定义工具
- calculator 仅允许数字、四则运算、括号、小数点和空格
- 工具执行失败不会导致进程 panic，错误作为 observation 回传给 LLM
- 不提供文件读写、shell、网络访问等高风险工具

---

## 6.5 思考过程持久化（v1.5.4）

Web Agent 流式回复的思考过程会落库，刷新页面后历史可还原（不再回退纯文本）。

- `messages` 表新增 `segments` JSON 列（GORM `serializer:json`，SQLite/PostgreSQL 通用），
  `content` 保留为纯文本投影（供复制 / 上下文拼接 / 搜索）
- `HandleSendMessageAgentStream` 在推 SSE 的同时按时序累积 `[]conversation.MessageSegment`
  （text/tool 交错），流结束调 `SaveAssistantMessageWithSegments` 落库
- 落库的工具结果与 SSE 推送共用 `sanitizeToolResult`，失败结果存「工具执行失败」，
  原始 error 不入库
- 这是「JSON 骨架 + 实体表」混合存储的碎片层。未来 artifact / 生成文件 / 异步任务等
  一等实体走独立表，segment 里放引用 id，不再改 messages 结构（详见演进路线图 v1.5.4）

---

## 7. 后续演进

- v1.6+：第三入口复用 Agent 能力
- v1.8+：异步任务抽象后支持微信端 Agent
- v2.x：用户自定义工具、插件系统、多 Agent 协作、RAG 工具
