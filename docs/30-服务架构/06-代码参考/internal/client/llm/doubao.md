# 架构说明：internal/client/llm/doubao.go

## 模块职责
字节豆包（火山引擎）API 客户端实现，实现 `LLMProvider` 接口。

## 结构体

```go
type DoubaoProvider struct {
    apiKey     string
    model      string
    httpClient *http.Client
}
```

## 方法

### NewDoubaoProvider
```go
func NewDoubaoProvider(apiKey, model string, timeout time.Duration) *DoubaoProvider
```
创建新的字节豆包客户端。

### ChatCompletion
```go
func (p *DoubaoProvider) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
```
实现 `LLMProvider` 接口：
1. 将内部 `ChatMessage` 转换为豆包要求的格式
2. 构建请求 JSON
3. 发送 HTTP 请求到 `https://ark.cn-beijing.volces.com/api/v3/chat/completions`
4. 解析响应
5. 检查 API 错误
6. 返回第一个 choice 的回复文本

## 请求格式
豆包请求格式（OpenAI 兼容）：
```json
{
  "model": "doubao-pro-128k",
  "messages": [
    {"role": "system", "content": "..."},
    {"role": "user", "content": "..."}
  ]
}
```

## 响应解析
- 成功从 `choices[0].message.content` 提取文本
- API 错误通过 `error` 字段检测，返回错误信息
- 日志记录 token 使用量

## 认证
- Authorization: Bearer {apiKey}
- API Key 在配置中获取

## 已实现能力
- ✅ 请求格式转换
- ✅ 响应解析
- ✅ 错误处理
- ✅ 超时控制
