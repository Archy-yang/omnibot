# 架构说明：internal/client/llm/

## 模块职责
大语言模型客户端，支持多个国内厂商，配置化选择，自动降级故障转移。

## 模块结构

| 文件 | 职责 |
|------|------|
| `types.go` | 定义统一接口 `LLMProvider` 和 `ChatMessage` |
| `factory.go` | `Client` 管理器，根据配置创建客户端，自动降级 |
| `qwen.go` | 阿里通义千问实现 |
| `doubao.go` | 字节豆包实现 |

## 核心接口

```go
// LLMProvider 大语言模型提供者统一接口
type LLMProvider interface {
    ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
}

// ChatMessage 单轮对话消息
type ChatMessage struct {
    Role    string // system / user / assistant
    Content string // 消息文本
}

// Client LLM 客户端管理器
type Client struct {
    defaultProvider    LLMProvider
    fallbackProviders []LLMProvider
}

// NewClient 根据配置创建 LLM 客户端
func NewClient(cfg config.LLMConfig) (*Client, error)

// ChatCompletion 对话补全，自动降级处理
func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
```

## 降级流程

```
Client.ChatCompletion → 尝试默认厂商
         ↓
      成功 → 返回结果
      失败 → 按顺序尝试 fallback 厂商
         ↓
   找到成功 → 返回结果
   全部失败 → 返回错误
```

## 配置支持

配置示例（yaml）：
```yaml
llm:
  providers:
    qwen:
      api_key: ${DASHSCOPE_API_KEY}
      model: qwen-max
      timeout: 30s
    doubao:
      api_key: ${DOUBAO_API_KEY}
      model: doubao-pro-128k
      timeout: 30s
  routing:
    default: qwen
    fallback_order: ["qwen", "doubao"]
```

## 已实现能力
- ✅ 统一 LLMProvider 接口
- ✅ 通义千问 API 对接
- ✅ 字节豆包 API 对接
- ✅ 配置化创建客户端
- ✅ 自动故障降级
- ✅ 每个厂商独立超时配置

## API 端点

- 通义千问: `https://dashscope.aliyuncs.com/api/v1/services/aigc/text-generation/generation`
- 字节豆包: `https://ark.cn-beijing.volces.com/api/v3/chat/completions`

## 依赖
- 标准库 `net/http`，不需要额外 SDK 依赖
