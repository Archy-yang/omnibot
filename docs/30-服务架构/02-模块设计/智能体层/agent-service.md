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
    Name        string
    Description string
    Parameters  map[string]interface{}
    Execute     func(ctx context.Context, args map[string]interface{}) (string, error)
}
```

### ReActAgent

```go
type ReActAgent struct {
    llmClient    LLMClient
    toolRegistry *ToolRegistry
    maxSteps     int
    timeout      time.Duration
    systemPrompt string
}
```

### AgentService

```go
func (s *AgentService) Run(ctx context.Context, userID int64, conversation []map[string]interface{}) (*AgentResult, error)
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
| POST | `/api/v1/chat/messages/agent/stream` | Agent SSE 流式对话 |

SSE 事件示例：

```text
event: agent_step
data: {"step":1,"tool_call":"calculator","tool_result":"42"}

data: {"content":"计算结果是 42。"}

data: [DONE]
```

---

## 6. 安全约束

- v1.5 仅注册系统内置工具，不支持用户自定义工具
- calculator 仅允许数字、四则运算、括号、小数点和空格
- 工具执行失败不会导致进程 panic，错误作为 observation 回传给 LLM
- 不提供文件读写、shell、网络访问等高风险工具

---

## 7. 后续演进

- v1.6+：第三入口复用 Agent 能力
- v1.8+：异步任务抽象后支持微信端 Agent
- v2.x：用户自定义工具、插件系统、多 Agent 协作、RAG 工具
