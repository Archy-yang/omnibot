# 统一消息通道接口

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.3+ |
| 最后更新 | 2026-05-16 |
| 状态 | ✅ 已实现 |

---

## 1. 这个接口解决什么问题？

**问题：** 每个聊天平台（微信、飞书、钉钉、Slack、Web）的消息发送 API 都不一样，如果每个平台都写一套独立的逻辑，业务层会重复 N 遍。

**方案：** 定义统一的 `MessageChannel` 接口，所有平台都实现这个接口。业务层只依赖接口，不关心具体平台的发送细节。

---

## 2. 读之前你需要知道什么？

需要先了解：
- [系统设计总览](../01-高层设计/01-系统设计总览.md) 的分层架构
- [用户通道关联服务](../用户域/user-channel-service.md)

---

## 3. 接口定义

```go
type MessageChannel interface {
    // ChannelType 返回通道类型标识
    // 例如："wechat", "feishu", "web", "slack"
    ChannelType() string

    // SendText 发送纯文本消息给用户
    // 对于同步通道（微信）：返回的是要在 HTTP 响应里返回的内容
    // 对于异步通道（飞书）：调用平台 API 发送消息
    SendText(channelUserID string, content string) error

    // SendReply 回复特定消息
    // 有些平台支持引用回复，有些不支持，不支持的可以降级为普通消息
    SendReply(channelMessageID string, channelUserID string, content string) error

    // IsAsync 返回这个通道是否支持异步发送
    // true = 可以后台发送，不需要在 HTTP 请求里直接返回
    // false = 必须在 HTTP 请求里同步返回响应内容
    IsAsync() bool
}
```

---

## 4. 通道注册与发现

### 工厂模式

所有通道实现都需要注册到全局工厂：

```go
// 注册通道
channel.Register(wechatChannel)

// 获取通道
ch, ok := channel.Get("wechat")

// 列出所有已注册的通道
types := channel.List()
```

### 通道生命周期

1. 应用启动时，各通道实现初始化并注册
2. Handler 层根据消息来源，从工厂获取对应通道
3. 业务层调用通道接口发送消息

---

## 5. 已实现的通道

### 微信公众号通道

**类型标识：** `wechat`

**特性：**
- ❌ 异步发送：不支持，必须同步返回 XML
- ✅ 被动回复：用户发消息后，5 秒内必须返回 XML
- ✅ 消息格式化：自动把纯文本包装成微信 XML 格式

**代码位置：** `internal/channel/wechat/`

---

## 6. 新通道接入 Checklist

接入一个新的对话通道，只需要做以下 4 件事：

### 1. 实现 MessageChannel 接口
```go
type MyNewChannel struct {
    // 配置项
}

func (c *MyNewChannel) ChannelType() string {
    return "my_new_channel"
}

func (c *MyNewChannel) SendText(channelUserID string, content string) error {
    // 调用你的平台 API 发送消息
}

func (c *MyNewChannel) SendReply(msgID, userID, content string) error {
    // 实现引用回复，或直接降级调用 SendText
}

func (c *MyNewChannel) IsAsync() bool {
    // 根据平台能力返回 true 或 false
}
```

### 2. 写 Handler 接收回调
在 `internal/api/` 下新建目录，写 HTTP Handler：
- 解析平台的请求格式
- 提取 `channelUserID`（平台内的用户 ID）
- 提取消息内容
- 调用统一的业务服务层

### 3. 注册到路由
在 `internal/api/routes.go` 里加一行路由注册。

### 4. 写单元测试
- 测试消息发送的各种场景
- 测试错误处理
- 测试边界情况（长文本、特殊字符等）

✅ **完成！** 其他所有东西（用户、记忆、LLM、配置）全部自动复用，零代码。

---

## 7. 设计决策记录

### 决策1：为什么接口这么简单？
- **背景**：要不要加更多方法？比如发图片、发卡片、发富媒体...
- **决策**：v1.3 只保留最核心的 4 个方法，其他能力后续扩展
- **原因**：
  - 不同平台的富媒体能力差异太大，抽象的成本很高
  - 文本消息是所有平台都支持的最小公约数
  - 先验证核心抽象是对的，再逐步扩展能力

### 决策2：为什么 SendText 返回 error 而不是 string？
- **背景**：微信需要返回 XML 字符串，飞书是调用 API 返回 error
- **决策**：统一返回 error，微信特殊处理放在微信自己的实现里
- **原因**：
  - 微信是特例（5秒必须响应），其他平台大多是异步 API
  - 特例不要污染接口抽象
  - 微信的特殊需求可以自己加方法，业务层需要的时候做类型断言

### 决策3：为什么通道自己不做重试？
- **背景**：发送失败要不要重试？
- **决策**：通道层不做重试，重试逻辑在上层统一处理
- **原因**：
  - 不同业务场景的重试策略不一样
  - 重试需要配合监控、告警、降级
  - 统一在上层做，更容易观测和调试

---

## 8. 下一步演进

- v1.4：增加飞书或 Web 通道，验证接口抽象的通用性
- v1.5：扩展富媒体能力（图片、卡片、按钮）
- v1.6：统一的重试、熔断、限流策略
- v2.0：支持通道动态插拔（运行时加载新通道）
