# 微信公众号通道

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.3+(v1.8 切到 UserChannels 通道路径 / v1.9 XML 协议下沉) |
| 最后更新 | 2026-06-24 |
| 状态 | ✅ 已实现 |

---

## 1. 这个文档解决什么问题？

说明微信公众号通道的实现细节、特性、限制、以及如何扩展。

读之前你需要知道：
- [统一消息通道接口](./message-channel.md)
- [用户通道关联服务](../用户域/user-channel-service.md)
- 微信公众号开发基础文档

---

## 2. 微信通道核心特性

### 同步响应限制

- **硬限制**：必须在 5 秒内返回 HTTP 响应
- **后果**：超过 5 秒微信会重试 3 次，用户会收到重复消息
- **策略**：v1.3 仍为同步响应，LLM 调用超时会返回兜底话术

### 消息类型支持

| 消息类型 | 支持状态 | 说明 |
|---------|---------|------|
| 文本消息 | ✅ 完全支持 | 对话、配置命令等 |
| 图片消息 | ⚠️ 基础支持 | 可以接收，回复固定话术 |
| 语音消息 | ⚠️ 基础支持 | 可以接收，回复固定话术 |
| 视频/短视频 | ⚠️ 基础支持 | 可以接收，回复固定话术 |
| 地理位置 | ⚠️ 基础支持 | 可以接收，回复固定话术 |
| 链接消息 | ⚠️ 基础支持 | 可以接收，回复固定话术 |
| 事件消息 | ✅ 完全支持 | 关注/取消关注/菜单点击 |

### 事件处理

| 事件 | 处理逻辑 |
|------|---------|
| subscribe（关注） | 自动创建用户 + 发送欢迎语 |
| unsubscribe（取消关注） | 记录日志，不删除数据 |
| 菜单点击 | 调用 LLM 生成相关回复 |

---

## 3. 通道实现细节

### 分层(v1.9):协议层在 channel 包,业务层在 handler 包

```
HTTP 入站 body
    ↓
channelwechat.Parse(body) → *InboundMessage      ← 协议层(channel 包)
    ↓
handler.dispatchMessage(in) → 纯文本 content       ← 业务层(api/wechat 包)
    ↓
wechatChannel.BuildResponseXML(toOpenID, fromGhID, content)  ← 协议层(channel 包)
    ↓
HTTP 出站 XML
```

handler 业务路径不感知 XML——所有 dispatch 函数签名 `(in *channelwechat.InboundMessage) (string, error)`,
返回纯文本(空串表示「不回复」,如 unsubscribe / view 事件)。XML 解析与序列化都在 channel 包做。

这种分层和 feishu / web 通道完全对齐——「业务出纯文本,通道层负责承载格式」。

### 核心数据结构

```go
// channelwechat.InboundMessage — 中性入站消息,handler 业务路径只看这个
type InboundMessage struct {
    ToUserName   string // 公众号微信号(响应时作为 FromUserName 回填)
    FromUserName string // 用户 OpenID(响应时作为 ToUserName 回填)
    CreateTime   int64
    MsgType      string // text / image / voice / video / location / link / event / ...
    Content      string // text 消息内容
    MsgID        string
    PicURL       string
    MediaID      string
    Event        string // subscribe / unsubscribe / CLICK / VIEW
    EventKey     string
    // ... 其他字段
}

// channelwechat.Channel — 无状态,纯通道协议适配器
type Channel struct{}
```

### MessageChannel 接口实现

| 方法 | 实现说明 |
|------|---------|
| `ChannelType()` | 返回 `"wechat"` |
| `SendText()` | 空实现,因为微信必须同步返回,不需要主动发 |
| `SendReply()` | 空实现,同上 |
| `IsAsync()` | 返回 `false`,同步通道 |

### 微信特有方法

因为微信是同步响应的特例,所以额外增加了通道独有的方法:

```go
// Parse 解析入站 XML body → 中性 InboundMessage
func Parse(body []byte) (*InboundMessage, error)

// BuildResponseXML 构建被动回复 XML(v1.9 纯函数化,无 channel 状态依赖)
func (c *Channel) BuildResponseXML(toOpenID, fromGhID, content string) string
```

返回格式:
```xml
<xml>
    <ToUserName><![CDATA[用户OpenID]]></ToUserName>
    <FromUserName><![CDATA[公众号微信号]]></FromUserName>
    <CreateTime>123456789</CreateTime>
    <MsgType><![CDATA[text]]></MsgType>
    <Content><![CDATA[回复内容]]></Content>
</xml>
```

---

## 4. 微信命令系统

用户可以通过发送特定格式的文本消息来控制机器人配置：

| 命令 | 功能 | 说明 |
|------|------|------|
| `#模型设置` | 显示配置引导菜单 | 列出所有可用命令 |
| `#设置Key sk-xxx` | 设置用户自己的 OpenAI API Key | AES-256-GCM 加密存储 |
| `#设置地址 https://xxx` | 设置自定义 API 端点 | 兼容所有 OpenAI 格式服务 |
| `#我的配置` | 查看当前配置 | API Key 脱敏显示（只显示前后 3 位） |
| `#重置模型` | 清除自定义配置 | 恢复使用系统默认模型 |
| `#记住 xxx` | 保存长期记忆 | 助手在后续对话中自动参考 |
| `#我的记忆` | 查看长期记忆 | 返回已保存的全部记忆列表 |
| `#清空记忆` | 清空长期记忆 | 删除当前用户全部长期记忆 |
| `#删除记忆 N` | 删除单条记忆 | 按序号删除第 N 条记忆 |

### 命令处理流程

```
用户发送消息
    ↓
是否以 # 开头？
    ├─ 是 → 解析命令
    │       ├─ LLM 配置命令 → 执行配置操作 → 返回结果
    │       └─ 长期记忆命令 → 执行记忆操作 → 返回结果
    └─ 否 → 正常对话流程
```

---

## 5. 设计决策记录

### 决策1：为什么微信通道的 SendText 是空实现？
- **背景**：微信必须在 HTTP 响应里返回 XML，不能主动发消息
- **决策**：SendText 是空实现，构建 XML 用微信独有的方法
- **原因**：
  - 这是平台特性差异，不应该污染通用接口
  - 其他通道不需要这个方法
  - 业务层可以通过类型断言来判断是否需要特殊处理

### 决策2：为什么用文本命令而不是菜单？
- **背景**：微信公众号可以设置自定义菜单，点击触发事件
- **决策**：用 # 开头的文本命令
- **原因**：
  - 文本命令不需要配置公众号后台，开箱即用
  - 文本命令可以在任何通道复用（飞书也可以用同样的命令格式）
  - 菜单有数量限制，命令可以无限扩展

### 决策3:为什么 v1.9 把 XML 协议下沉到 channel 包?

- **背景**:v1.6 飞书接入 + v1.8 UserChannels 统一后,三入口里唯独 wechat handler 还在
  内联 XML 解析/序列化(`buildResponse` 调 11 处,XML 模板硬编码在业务函数里)
- **决策**:
  - 入站:`channelwechat.Parse(body)` → 中性 `InboundMessage`,handler 业务路径只看中性结构
  - 出站:handler dispatch 返回纯文本,HTTP 顶层用 `wechatChannel.BuildResponseXML` 包装
- **原因**:
  - 业务逻辑和协议适配应该分层——和 feishu/web 通道对齐
  - 测试更聚焦:dispatch 测纯文本(简单 string 比对),HTTP 端到端测 XML 包装(已有覆盖)
  - 后续若加新通道协议(企业微信、Telegram),复用「中性 InboundMessage + 通道层翻译」模式

### 决策4:为什么 `Channel.BuildResponseXML` 改为纯函数,删除 toUserName 字段?

- **背景**:旧实现 `Channel.toUserName` 存公众号微信号,需要 `SetToUserName` 切换
- **决策**:`BuildResponseXML(toOpenID, fromGhID, content)` 三参纯函数,channel 完全无状态
- **原因**:
  - 公众号微信号本来就在入站请求的 `<ToUserName>` 里——不需要预存
  - 多公众号场景下不需要 channel 切换状态
  - 纯函数测试更直接

### 决策5:为什么 API Key 只显示前后3位?

- **背景**:用户需要确认自己设置的 Key 对不对,但又不能泄露完整 Key
- **决策**:脱敏显示,只显示前后各 3 位,中间用 ... 代替
- **原因**:
  - 微信聊天记录可能被截图、转发
  - 够用原则:用户只需要确认自己输的大概对不对
  - 如果真的记错了,重新设置就行,不需要看完整的 Key

---

## 6. 常见问题

### Q: 微信响应超时怎么办？
**A**: v1.3 目前是同步调用 LLM，如果 LLM 超时超过 5 秒，会返回兜底话术：「思考中，请稍候再试...」。未来会改成「首次返回提示，用户再发任意消息触发结果」的模式。

### Q: 微信会重试消息吗？
**A**: 会，微信最多重试 3 次。所以消息去重很重要，v1.2 已经实现了基于 MsgID 的去重机制。

### Q: 可以主动给用户发消息吗？
**A**: 认证服务号可以，但订阅号不行。我们目前的实现是针对订阅号优化的，暂不支持客服消息。

---

## 7. 下一步演进

- v1.4：支持「首次返回提示，第二次请求返回结果」的伪异步模式
- v1.5：支持微信客服消息接口（服务号专属），真正异步发送
- v1.6：支持微信富媒体消息（图片、语音转文字）
