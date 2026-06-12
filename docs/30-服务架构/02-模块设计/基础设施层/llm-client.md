# LLM 客户端

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.3+ |
| 最后更新 | 2026-06-13 |
| 状态 | ✅ 已实现 |

---

## 1. 这个模块解决什么问题？

**核心问题**：
1. 不同大模型厂商（OpenAI、通义千问、字节豆包）的 API 格式各不相同
2. 需要统一的调用接口，业务层不应该关心底层是哪个模型
3. 需要自动降级：默认模型失败时自动切换到备用模型
4. Web 端需要流式输出（打字机效果），而不是等待完整响应

**解决方案**：
统一的 `LLMProvider` 接口 + 工厂模式创建 + Client 降级管理器，支持同步和流式两种调用方式。

---

## 2. 核心接口

```go
type LLMProvider interface {
    ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
    StreamChatCompletion(ctx context.Context, messages []ChatMessage) (<-chan StreamChunk, error)
}

type StreamChunk struct {
    Content string
    Done    bool
    Error   error
}
```

### ChatCompletion（同步）

阻塞调用，等待 LLM 完整响应后返回。适用于微信等同步通道。

### StreamChatCompletion（流式）

返回只读 channel，逐片推送增量文本。调用方持续读取直到 channel 关闭或收到 `Done: true`。适用于 Web SSE 流式输出。

---

## 3. Provider 实现

### OpenAI Provider

`internal/client/llm/openai.go`

| 特性 | 说明 |
|------|------|
| 协议 | OpenAI Chat Completions API |
| 端点 | `{baseURL}/chat/completions` |
| 同步 | `stream: false`，JSON 解析 `choices[0].message.content` |
| 流式 | `stream: true`，`bufio.Scanner` 逐行解析 `data: {...}` SSE 格式 |
| 兼容 | 所有 OpenAI 兼容 API（Azure、DeepSeek、本地 vLLM 等） |

流式实现细节：

```
1. 发送 POST，设置 stream: true
2. 启动 goroutine 读取 resp.Body
3. bufio.Scanner 逐行扫描
4. 跳过空行和非 data: 前缀行
5. 遇到 data: [DONE] → 发送 StreamChunk{Done: true}，关闭 channel
6. 解析 JSON → 提取 choices[0].delta.content → 发送 StreamChunk{Content: "..."}
7. 监听 ctx.Done()，支持客户端取消
```

### 字节豆包 Provider

`internal/client/llm/doubao.go`

| 特性 | 说明 |
|------|------|
| 协议 | 火山引擎豆包 API |
| 端点 | `https://ark.cn-beijing.volces.com/api/v3/chat/completions` |
| 同步 | ✅ 支持 |
| 流式 | ❌ 返回 `ErrStreamingNotSupported` |

### 通义千问 Provider

`internal/client/llm/qwen.go`

| 特性 | 说明 |
|------|------|
| 协议 | 阿里云 DashScope API |
| 端点 | `https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation` |
| 同步 | ✅ 支持 |
| 流式 | ❌ 返回 `ErrStreamingNotSupported` |

---

## 4. Client 降级管理器

`internal/client/llm/factory.go`

```go
type Client struct {
    defaultProvider    LLMProvider
    fallbackProviders  []LLMProvider
}
```

### 降级流程

```
ChatCompletion 调用
    ↓
默认 provider.ChatCompletion()
    ├─ 成功 → 返回结果
    └─ 失败 → 依次尝试 fallbackProviders[0], [1], ...
              ├─ 某个成功 → 返回结果
              └─ 全部失败 → 返回 "all providers failed"
```

### StreamChatCompletion

流式不支持降级（降级需要完整的 fallback provider 也支持流式，当前只有 OpenAI）。直接委托给 `defaultProvider`。

---

## 5. 用户自定义配置

用户可以通过微信命令或 Web API 配置自己的 LLM：

```go
type UserConfig struct {
    Provider string // openai/qwen/doubao
    APIKey   string
    BaseURL  string
    Model    string
}
```

`NewClientFromUserConfig` 根据用户配置创建单 provider 客户端（无 fallback）。

优先级：用户自定义配置 > 系统默认配置。

---

## 6. 设计决策记录

### 决策1：为什么用 channel 而不是 callback？

- **决策**：`StreamChatCompletion` 返回 `<-chan StreamChunk`
- **原因**：
  - Go 惯用模式，天然支持 `range`、`select` 超时
  - 调用方可以用 `select { case chunk := <-ch: ... case <-ctx.Done(): ... }` 实现超时
  - goroutine 内部自动管理生命周期

### 决策2：为什么 Doubao/Qwen 暂不支持流式？

- **决策**：返回 `ErrStreamingNotSupported`
- **原因**：
  - 豆包和千问的流式 SSE 格式与 OpenAI 有差异，需要分别适配
  - 当前系统默认 provider 为 OpenAI，流式主要服务 Web 端
  - 后续按需实现，接口已预留

### 决策3：为什么保留同步端点？

- **决策**：`POST /api/v1/chat/messages` 和 `POST /api/v1/chat/messages/stream` 并存
- **原因**：
  - 微信通道必须同步（5 秒限制）
  - 非 OpenAI provider 用户降级到同步
  - 渐进式迁移，不破坏现有功能

---

## 7. 测试策略

| 测试 | 说明 |
|------|------|
| OpenAI 同步 | httptest mock server 返回 JSON，验证解析 |
| OpenAI 流式 | httptest mock server 发送 SSE chunks，验证 channel 输出 |
| 流式空响应 | 立即收到 `[DONE]`，验证空内容 |
| 流式 ctx 取消 | 发送一个 chunk 后 cancel，验证错误传播 |
| 流式 HTTP 错误 | 无效地址，验证返回错误 |
| Doubao/Qwen 不支持 | 验证返回 `ErrStreamingNotSupported` |
| 降级机制 | mock 默认 provider 失败，验证 fallback |

---

## 8. 下一步演进

- v1.4：Doubao/Qwen 流式支持
- v1.5：Token 计数 + 动态截断
- v1.6：请求重试 + 指数退避
