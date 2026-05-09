# 架构说明：internal/client/llm/qwen.go

## 模块职责
阿里通义千问 API 客户端实现，实现 `LLMProvider` 接口。

## 结构体

```go
type QwenProvider struct {
    apiKey     string
    model      string
    httpClient *http.Client
}
```

## 方法

### NewQwenProvider
```go
func NewQwenProvider(apiKey, model string, timeout time.Duration) *QwenProvider
```
创建新的通义千问客户端。

### ChatCompletion
```go
func (p *QwenProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
```
实现 `LLMProvider` 接口：
1. 将内部 `ChatMessage` 转换为通义千问要求的格式
2. 构建请求 JSON
3. 发送 HTTP 请求到 `https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation`
4. 解析响应
5. 检查 API 错误
6. 返回生成的文本

## 请求格式
通义千问请求格式：
```json
{
  "model": "qwen-max",
  "input": {
    "messages": [
      {"role": "system", "content": "..."},
      {"role": "user", "content": "..."}
    ]
  }
}
```

## 响应解析
- 成功从 `output.text` 提取文本
- API 错误通过 `code` 字段检测，返回错误信息
- 日志记录 token 使用量

## 认证
- Authorization: Bearer {apiKey}
- API Key 在配置中获取

## 已实现能力
- ✅ 请求格式转换
- ✅ 响应解析
- ✅ 错误处理
- ✅ 超时控制
