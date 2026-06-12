# Web 对话通道

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.4+ |
| 最后更新 | 2026-06-13 |
| 状态 | ✅ 已实现 |

---

## 1. 这个文档解决什么问题？

说明 Web 对话通道的实现细节、接口设计、以及与微信通道的差异。

读之前你需要知道：
- [统一消息通道接口](./message-channel.md)
- [消息与上下文记忆服务](../对话域/message-service.md)
- [长期记忆服务](../对话域/memory-service.md)
- 产品 PRD：`docs/20-产品PRD/进行中/v1.4-Web对话页面PRD.md`

---

## 2. Web 通道核心特性

### 与微信通道的对比

| 特性 | 微信通道 | Web 通道 |
|------|---------|---------|
| 同步响应 | ✅ 5 秒限制 | ✅ 无限制 |
| 流式输出 | ❌ 不支持 | ✅ SSE 打字机效果 |
| 富媒体 | ❌ 纯文本 | ✅ Markdown、代码高亮 |
| 用户识别 | OpenID 自动关联 | 匿名会话 ID（localStorage） |
| 消息去重 | 微信 MsgID | 暂不需要（浏览器不重试） |
| 异步发送 | ❌ | ✅ `IsAsync()=true` |

### 用户识别策略

**匿名会话 + 浏览器本地存储**：
- 首次访问时前端生成 UUID 作为 `session_id`
- 存入 `localStorage`，刷新不丢失
- 后端调用 `GetOrCreateByChannel("web", session_id)` 获取或创建用户
- 无需注册登录，无隐私问题

---

## 3. API 接口

### 聊天接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/v1/chat/messages` | 同步对话（等待完整响应） |
| `POST` | `/api/v1/chat/messages/stream` | SSE 流式对话（逐 token 推送） |
| `GET` | `/api/v1/chat/messages` | 获取历史消息 |

### 同步对话

```
POST /api/v1/chat/messages
Body: { "session_id": "...", "content": "你好" }

Response (JSON):
{
  "success": true,
  "data": {
    "id": 123,
    "role": "assistant",
    "content": "你好！有什么可以帮你的？",
    "created_at": "2026-06-13T12:00:00Z"
  }
}
```

流程与微信通道完全一致：获取用户 → 保存消息 → 构建上下文 → 调用 LLM → 保存回复 → 返回 JSON。

### SSE 流式对话

```
POST /api/v1/chat/messages/stream
Body: { "session_id": "...", "content": "你好" }
Headers: Accept: text/event-stream

Response (SSE):
data: {"content":"你"}
data: {"content":"好"}
data: {"content":"！"}
data: {"content":"有"}
data: {"content":"什么"}
data: {"content":"可以"}
data: {"content":"帮你"}
data: {"content":"的"}
data: {"content":"？"}
data: [DONE]
```

**实现要点**：
1. 设置响应头 `Content-Type: text/event-stream`、`Cache-Control: no-cache`
2. 调用 `activeClient.StreamChatCompletion()` 获取 channel
3. 每收到一个 chunk，写入 `data: {"content":"..."}\n\n` 并 `Flusher.Flush()`
4. 流结束后写入 `data: [DONE]\n\n`
5. 后台 goroutine 中收集完整内容，流结束后保存到数据库
6. 流式错误通过 `data: {"error":"..."}` 返回

**降级**：
- 用户使用非 OpenAI provider 时 `StreamChatCompletion` 返回 `ErrStreamingNotSupported`
- 前端检测到 HTTP 错误后降级调用同步端点
- 同步端点保留不变，作为 fallback

### 记忆管理接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/memories` | 获取记忆列表 |
| `POST` | `/api/v1/memories` | 新增记忆 |
| `DELETE` | `/api/v1/memories` | 清空全部记忆 |
| `DELETE` | `/api/v1/memories/:id` | 删除单条记忆 |
| `PUT` | `/api/v1/memories/:id` | 更新单条记忆 |

### LLM 配置接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/user/llm-config` | 查看当前配置 |
| `PUT` | `/api/v1/user/llm-config` | 更新配置 |
| `DELETE` | `/api/v1/user/llm-config` | 清除配置 |

---

## 4. 前端架构

### 分层结构

```
ChatPage.vue (页面)
    ↓
useChat composable (逻辑封装)
    ↓
useChatStore (Pinia store, 状态管理)
    ↓
chatService (API 调用层)
    ↓
fetch / axios (HTTP 客户端)
```

### 流式调用流程

```
用户点击发送
    ↓
store.sendMessage(content)
    ↓
立即添加 user message 到列表
    ↓
添加空 content 的 assistant 占位消息
    ↓
chatService.sendMessageStream(content, sessionId, {
    onChunk: (text) => { assistantMessage.content += text },
    onDone:  () => { isLoading = false },
    onError: (err) => { 移除占位消息, 显示错误 }
})
    ↓
fetch POST /api/v1/chat/messages/stream
    ↓
ReadableStream.getReader() 逐块读取
    ↓
解析 data: {...} 行，调用 onChunk
    ↓
遇到 [DONE] 调用 onDone
```

### 为什么用 fetch 而不是 axios？

axios 基于 XHR，不支持 `ReadableStream`。流式读取必须用原生 `fetch` API。

---

## 5. Channel 实现

`internal/channel/web/channel.go`

```go
type Channel struct{}

func (c *Channel) ChannelType() string { return "web" }
func (c *Channel) IsAsync() bool       { return true }
func (c *Channel) SendText(...) error  { return nil }  // HTTP response handles it
func (c *Channel) SendReply(...) error { return nil }  // HTTP response handles it
```

`SendText` / `SendReply` 为空实现，因为 Web 通道通过 HTTP response 直接返回内容，不需要主动推送。

`IsAsync()=true` 表示支持异步模式，为后续 WebSocket 实时推送预留。

---

## 6. 设计决策记录

### 决策1：为什么 SSE 而不是 WebSocket？

- **决策**：用 SSE（Server-Sent Events）
- **原因**：
  - LLM 流式输出是单向的（服务端 → 客户端），不需要双向通信
  - SSE 基于标准 HTTP，不需要升级协议，穿透代理/防火墙更简单
  - 浏览器原生 `EventSource` / `fetch` + `ReadableStream` 支持
  - 实现更轻量，不需要 WebSocket 库

### 决策2：为什么匿名会话而不是登录？

- **决策**：前端生成 UUID 作为会话 ID，不需要注册登录
- **原因**：
  - 降低使用门槛，打开即用
  - Web 对话是辅助入口，微信是主入口
  - 后续可通过扫码绑定微信账号实现跨通道打通
  - 不收集用户个人信息

### 决策3：为什么保留同步端点？

- **决策**：流式端点 `/messages/stream` 和同步端点 `/messages` 并存
- **原因**：
  - 非 OpenAI provider 用户降级到同步
  - 渐进式迁移，不破坏现有功能
  - 未来可能有不需要流式的场景（如 API 集成）

---

## 7. 部署

前端静态资源通过 Go Embed 嵌入到二进制文件中：

```
frontend/dist/ → Go embed → 单二进制文件 → scp 到服务器直接运行
```

不需要 nginx、不需要 node、不需要 CDN。访问 `/chat/` 即可使用。

---

## 8. 下一步演进

- v1.4+：WebSocket 实时推送（双向通信场景）
- v1.5：扫码绑定微信账号，跨通道记忆打通
- v1.5：可视化 LLM 配置页面（替代命令 `#设置Key`）
- v1.6：暗色模式、代码高亮、消息导出
