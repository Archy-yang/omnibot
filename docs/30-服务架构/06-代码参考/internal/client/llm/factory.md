# 架构说明：internal/client/llm/factory.go

## 模块职责
根据配置创建 LLM 客户端，管理默认厂商和降级列表，提供统一入口，自动处理降级。

## 核心结构体

```go
type Client struct {
    defaultProvider    LLMProvider
    fallbackProviders []LLMProvider
}
```

## 核心方法

### NewClient
```go
func NewClient(cfg config.llmConfig) (*Client, error)
```
- 读取配置，创建所有配置的厂商实例
- 验证默认厂商存在
- 收集降级厂商列表
- 返回客户端实例

### ChatCompletion
```go
func (c *Client) ChatCompletion(ctx context.Context, messages []ChatMessage) (string, error)
```
- 先尝试默认厂商
- 成功直接返回
- 失败写入日志，依次尝试降级厂商
- 找到成功返回结果
- 全部失败返回错误

## 厂商名称匹配
支持别名：
- `qwen` / `tongyi` / `alibabacloud` → 通义千问
- `doubao` / `byteDance` / `volcengine` → 字节豆包

## 超时处理
- 每个厂商独立配置超时时间
- 超时解析失败使用默认 30 秒
- 超时通过 `context` 传递给底层 HTTP 请求

## 日志
- 默认厂商失败输出警告日志
- 降级尝试输出信息日志
- 降级失败输出警告日志
