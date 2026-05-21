# 架构说明：internal/client/llm/types.go

## 模块职责
定义大语言模块统一接口和公共类型。

## 导出定义

### ChatMessage
```go
type ChatMessage struct {
    Role    string // system / user / assistant
    Content string // 消息文本
}
```

### LLMProvider
```go
type LLMProvider interface {
    ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
}
```
接口方法：
- `ChatCompletion` - 对话补全，输入对话历史，输出生成文本
- 传入 `context.Context` 支持超时取消

## 设计要点
- 接口抽象使得上层业务与具体厂商解耦
- 新增厂商不需要修改上层代码，只需要新增实现
- 单元测试可以 mock 接口
