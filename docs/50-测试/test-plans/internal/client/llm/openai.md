# 测试计划：internal/client/llm/openai.go

## 测试目的

验证 OpenAI API 兼容客户端正确转换请求格式和解析响应，支持 OpenAI 官方、Azure OpenAI 及其他兼容服务。

## 测试环境

- 不实际调用 API，使用 `net/http/httptest` 模拟 HTTP 服务器
- 遵循项目 TDD 规范：测试先行，实现跟进

---

## 方法：TestOpenAI_NewProvider

**测试目的**：验证 NewOpenAIProvider 构造函数正确初始化配置。

**输入参数**：
- apiKey: "test-key"
- baseURL: "https://api.openai.com/v1"
- model: "gpt-3.5-turbo"
- timeout: 30 * time.Second

**期望断言**：
- 返回的 provider 不为 nil
- apiKey、baseURL、model 字段正确保存
- http.Client 具有正确的超时配置

---

## 方法：TestOpenAI_ParseResponse

**测试目的**：正确解析 OpenAI API 成功响应，提取回复文本。

**输入参数**：
- 模拟 JSON 响应：
```json
{
  "id": "chatcmpl-123",
  "object": "chat.completion",
  "created": 1677652288,
  "model": "gpt-3.5-turbo",
  "choices": [{
    "index": 0,
    "message": {
      "role": "assistant",
      "content": "这是大模型生成的回复"
    },
    "finish_reason": "stop"
  }],
  "usage": {
    "prompt_tokens": 9,
    "completion_tokens": 12,
    "total_tokens": 21
  }
}
```

**期望断言**：
- 正确提取 "这是大模型生成的回复" 文本
- 不返回错误

---

## 方法：TestOpenAI_ParseError

**测试目的**：API 返回错误时，正确返回错误信息。

**输入参数**：
- 模拟错误 JSON 响应：
```json
{
  "error": {
    "message": "Invalid API key provided",
    "type": "invalid_request_error",
    "code": "invalid_api_key"
  }
}
```

**期望断言**：
- 返回 error
- 错误信息包含 "Invalid API key provided"

---

## 方法：TestOpenAI_HTTPRequest

**测试目的**：使用 httptest 模拟服务器，验证 HTTP 请求格式正确。

**输入参数**：
- 使用 httptest.NewServer 创建模拟服务器
- 模拟服务器验证：
  - 请求方法为 POST
  - 请求路径为 /chat/completions
  - Header 包含 `Authorization: Bearer test-key`
  - Header 包含 `Content-Type: application/json`
  - 请求体包含 model 和 messages 字段

**期望断言**：
- 请求格式符合 OpenAI API v1 规范
- 正确解析模拟服务器返回的响应

---

## 方法：TestOpenAI_WithBaseURL

**测试目的**：验证自定义 base_url 功能（支持 Azure 和其他兼容服务）。

**输入参数**：
- baseURL: "https://custom-endpoint.com/v1"
- 模拟服务器验证请求发送到该地址

**期望断言**：
- 请求发送到自定义 base_url 指定的地址
- 响应正确解析

---

## 已实现状态

- ✅ 功能实现完成
- ✅ 单元测试全部通过
