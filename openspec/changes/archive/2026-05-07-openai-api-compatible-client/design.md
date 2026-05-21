## Context

**当前状态**：
- 系统已有统一的 `LLMProvider` 接口：`ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)`
- 已实现两个 Provider：Qwen（通义千问）、Doubao（字节豆包）
- 每个 Provider 独立维护 HTTP 请求构造和响应解析
- 工厂模式通过 provider name 创建对应实例

**约束条件**：
- 必须实现现有 `LLMProvider` 接口，保持上层业务代码零改动
- 不引入第三方 SDK（与现有 Qwen/Doubao 保持一致：直接使用标准库 net/http）
- 支持配置 `base_url` 以兼容 Azure OpenAI 和其他兼容层
- 必须正确处理 OpenAI API 的错误响应格式

## Goals / Non-Goals

**Goals:**
- 实现 OpenAI API 兼容的 LLM Provider
- 支持 OpenAI 官方（gpt-3.5-turbo、gpt-4 等）
- 支持 Azure OpenAI 服务
- 支持所有遵循 OpenAI API 协议的开源模型服务
- 正确处理 API 错误响应
- 与现有 qwen/doubao provider 结构保持一致

**Non-Goals:**
- 不实现 OpenAI 的其他 API（Embeddings、Fine-tuning、Images 等）
- 不引入 openai-go SDK，保持纯标准库 HTTP 调用
- 不实现流式响应（SSE），保持与现有接口一致的非阻塞调用
- 不实现 Function Calling 功能（后续迭代可扩展）

## Decisions

### 1. 请求格式遵循 OpenAI v1 规范

**决策**：
```json
POST /chat/completions
{
  "model": "gpt-3.5-turbo",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."}
  ],
  "temperature": 0.7,
  "stream": false
}
```

**理由**：与现有 qwen/doubao provider 的请求结构保持一致，便于上层统一处理。

**备选方案**：支持额外参数（如 max_tokens、temperature）→ 后续可通过配置扩展，当前使用默认值。

### 2. Provider Name 映射

**决策**：
- `"openai"`: 标准 OpenAI API
- `"azure"`: Azure OpenAI Service（使用同样的实现类，通过 base_url 区分）

**理由**：Azure OpenAI 的请求/响应格式与 OpenAI 完全兼容，只需不同的 base_url 和认证方式。

### 3. 认证方式

**决策**：
```go
req.Header.Set("Authorization", "Bearer "+c.apiKey)
// Azure 支持通过 api-key header 或 Authorization Bearer，统一使用 Bearer
```

**理由**：标准 OpenAI 和新的 Azure OpenAI 都支持 Bearer token 方式，实现最简单。

### 4. 响应解析

**决策**：
- 成功响应：`choices[0].message.content`
- 错误响应：`error.message` 字段提取错误信息

**理由**：与现有 provider 的错误处理模式一致，上层不需要感知不同厂商的错误格式。

### 5. 实现结构与现有 Provider 对齐

**决策**：
```go
type OpenAIProvider struct {
    apiKey  string
    baseURL string
    model   string
    client  *http.Client
}

func NewOpenAIProvider(apiKey, baseURL, model string, timeout time.Duration) *OpenAIProvider
```

**理由**：与 QwenProvider、DoubaoProvider 的构造函数签名一致，工厂模式统一处理。

## Risks / Trade-offs

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| 不同兼容层响应格式有细微差异 | 部分第三方兼容层可能不返回标准字段 | 增加严格的 nil 检查和默认值处理 |
| OpenAI API 版本升级 | v2 格式变化可能导致不兼容 | 固定使用 v1 端点，文档注明兼容版本 |
| 超长上下文 token 超限 | API 返回 400 错误 | 统一降级处理，由上层记录错误日志 |
| Azure Deployment ID 配置 | Azure 需要部署名而非标准 model 名 | 在文档中说明配置方式，使用 model 字段承载 deployment id |

## Migration Plan

**部署步骤**：
1. 代码发布，不修改默认路由（默认仍使用 qwen/doubao）
2. 在配置文件中添加 openai provider 配置
3. 验证可用后，可通过修改 `llm.routing.default` 切换为 openai

**回滚策略**：
- 纯新增代码，无破坏性变更，回滚只需还原配置文件的 default provider
