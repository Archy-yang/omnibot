# OpenAI API 兼容 Provider (internal/client/llm/openai.go)

## 模块职责

实现 OpenAI API v1 协议的大模型提供者，支持：
- OpenAI 官方 API（GPT-3.5, GPT-4 等）
- Azure OpenAI Service
- 所有兼容 OpenAI API 协议的开源模型服务（如 Llama, Qwen, ChatGLM 等）

## 核心结构

```go
type OpenAIProvider struct {
    apiKey  string        // API 密钥
    baseURL string        // API 基础地址（可自定义）
    model   string        // 模型名称
    client  *http.Client  // HTTP 客户端（带超时）
}
```

### 构造函数

```go
func NewOpenAIProvider(apiKey, baseURL, model string, timeout time.Duration) *OpenAIProvider
```

- `baseURL` 可选，默认为 `https://api.openai.com/v1`
- Azure OpenAI 使用自定义 baseURL

## 请求/响应结构

### 请求格式

```json
POST /chat/completions
{
  "model": "gpt-3.5-turbo",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."}
  ],
  "stream": false
}
```

### 响应格式

```json
{
  "choices": [{
    "message": {
      "content": "回复内容"
    }
  }]
}
```

### 错误格式

```json
{
  "error": {
    "message": "错误信息",
    "type": "错误类型",
    "code": "错误码"
  }
}
```

## 公开接口

实现 `LLMProvider` 接口：

```go
ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
```

## 配置示例

### OpenAI 官方

```yaml
llm:
  providers:
    openai:
      api_key: "sk-xxx"
      base_url: "https://api.openai.com/v1"  # 可选，默认即为该值
      model: "gpt-3.5-turbo"
      timeout: "30s"
```

### Azure OpenAI

```yaml
llm:
  providers:
    azure:
      api_key: "xxx"
      base_url: "https://your-resource.openai.azure.com/openai/deployments/gpt-35"
      model: "gpt-35-turbo"
      timeout: "30s"
```

## 依赖项

- `context` - 上下文传递，支持请求取消
- `net/http` - HTTP 客户端
- `encoding/json` - JSON 序列化
- `pkg/logger` - 结构化日志

## 日志

成功调用日志：
- `model`: 使用的模型名称
- `duration`: 请求耗时

失败日志：
- `error`: 错误信息
- `error_type`: API 返回的错误类型
- `error_code`: API 返回的错误码
- `status_code`: HTTP 状态码

## 测试覆盖

- `TestOpenAI_NewProvider`: 构造函数初始化
- `TestOpenAI_ParseResponse`: 成功响应解析
- `TestOpenAI_ParseError`: 错误响应解析
- `TestOpenAI_HTTPRequest`: HTTP 请求格式验证
- `TestOpenAI_WithBaseURL`: 自定义端点验证

使用 `httptest` 模拟 API 服务器，不产生真实网络请求。

## 与现有 Provider 对比

| Provider | 接口风格 | 支持厂商 |
|----------|----------|----------|
| Qwen | 阿里云通义千问 | 通义千问 |
| Doubao | 字节豆包 | 字节跳动 |
| OpenAI | OpenAI 兼容 | 所有兼容 OpenAI API v1 的服务 |
