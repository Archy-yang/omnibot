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

> **思考模式(v2.x,方案5)新增两个标记事件:**
> - `event: thought` -- 思考轮标记。ReAct 一轮 LLM 流结束后若该轮有 tool_call(思考轮),
>   后端发 thought,携带该轮思考文本。前端据此把该轮 token 从主气泡迁移到灰色思考块。
> - `event: final` -- 回复轮标记。一轮无 tool_call(回复轮)时发 final,携带最终回复文本。
>   前端据此确认主气泡内容 + 落库 content。
>
> 设计动机:思考 vs 回复的语义由后端在轮末**显式标记**,前端不再靠「最后一个 text 段」
> 推断(不可靠)。token 流式时先乐观进主气泡,思考轮末的 thought 事件触发迁移--简单问题
> (单轮回复)无 thought,主气泡零跳动。详见 §6.5 思考模式展示。

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

### 6.5.1 思考模式展示(v2.x,方案5)

思考过程与最终回复分离展示:思考进灰色可折叠思考块,最终回复进主气泡。

**事件层(后端 RunStream):**
- `AgentEventFinal`:回复轮(无 tool_call)末发出,Content=最终回复。异常结束(超时/maxSteps)
  也发 Final(兜底文案),保证消费方总收到一次。LLM 失败走 Error 不发 Final。
- `AgentEventThought`:思考轮(有 tool_call)末发出,Content=该轮思考文本。与 Final 对称。

**段语义(MessageSegment.Role):**
- `text` 段带 `role`:`thought`(思考过程) | `final`(最终回复)
- `tool` 段天然是思考过程,不用 role
- handler 收到 Thought 把当前 text 段标 thought,收到 Final 标 final;落库 segments 带 role,
  历史回看按 role 分拆渲染

**前端展示(ChatMessage.vue):**
- 思考块 = 所有 role!=="final" 的段(thought text + 全部 tool);主气泡 = role==="final" 的段
- 流式时(streaming=true)思考块展开实时显示过程,结束自动收起成「已思考 N 步」
- 操作栏(复制/点赞/踩)流式中隐藏,回复完成才显示

**方案5 的迁移设计(token 默认进主气泡):**
token 流式时先乐观进 final 段(主气泡实时显示);思考轮末收到 thought 后,该轮文本段被
改标 thought,从主气泡迁移到思考块。简单问题(单轮回复)无 thought 事件,主气泡零跳动;
多轮工具的思考文本短暂在主气泡后迁移。这避免「一开始就把回复当思考状态显示」的体验问题。

**IM 路径(飞书/微信):** AgentService.Run 聚合时,Final 事件 -> FinalResponse(只含最终回复,
不含思考文本);Thought 事件忽略。IM 用户只收到最终回复,不展示思考块。

**为 reasoning_content(模型原生思考)预留:** 将来支持 DeepSeek-R1 等模型的
`delta.reasoning_content` 字段时,reasoning 进 thought 段、content 进 final 段,
复用同一套 role 机制,无需改展示层。

---

## 6.6 Agent 运行链路记录（v1.5.5）

「实体层」首次落地。`agent_steps` 独立表把一轮对话的**完整执行链**按顺序记下来——
不只工具调用，连每次 LLM 调用也记。一轮 ReAct 由有序步骤组成（llm_call / tool_call），
`WHERE message_id=? ORDER BY seq` 一句还原完整时序。

与 segments 的脱敏展示分工：

| 维度 | `segments[].result`（展示） | `agent_steps`（记录） |
|------|------|------|
| 内容 | 脱敏（失败显「工具执行失败」） | 完整原始（含真实错误、prompt、模型回复） |
| 范围 | 仅工具结果 | 整条链：每次 LLM 调用 + 每次工具调用 |
| 诉求 | 轻量、安全 | 完整、可分析、可复盘 |
| 暴露 | 对外（前端展开看） | 内部表，不对外 |

`agent_steps` 字段：`user_id` / `message_id`（锚到这一轮的 assistant 消息）/ `seq`（链内顺序）/
`kind`（llm_call|tool_call）/ `status` / `duration_ms` / `tool`（tool 用）/ `model`（llm 用）/
`request`（llm: messages JSON；tool: arguments）/ `response`（llm: {content,tool_calls}；
tool: 原始未脱敏结果）/ `prompt_tokens` / `completion_tokens`（预留，本轮恒 0）/ `created_at`。

数据流：`RunStream` 每轮调 LLM 前快照 messages、计时，轮末 emit `AgentEventLLMCall`；
工具执行处 emit `AgentEventToolResult`（原始结果+arguments+status+duration）→
handler 按时序累积 `[]AgentStep`（带 seq）→ `SaveAssistantMessageWithSegments` 保存消息后
stamp `MessageID` 批量落库（非原子，记录失败仅记日志，不拖垮主消息）。

**运行时与记录是两条独立的路**：运行时上下文由滑动窗口（最近10轮、纯文本）框住，工具结果是
「轮内消耗品」、跨轮只留最终文本结论；`agent_steps` 纯离线，不进运行时上下文、不影响模型看到
什么、不影响上下文成本。所以记录表按「分析最好查」设计，放开手记全。

> **为什么记整条链而非只记工具，又为何不截断**：只记工具看不到「模型为什么这么走」；
> 截断大结果则丢了「记录 + 分析」要的完整数据。正解是两条路分离——运行时瘦、记录全。
> 分析能力（按 kind/status/tool/耗时聚合、token 成本）对接 v1.8+。

---

## 7. 后续演进

- v1.6+：第三入口复用 Agent 能力
- v1.8+：异步任务抽象后支持微信端 Agent
- v2.x：用户自定义工具、插件系统、多 Agent 协作、RAG 工具
