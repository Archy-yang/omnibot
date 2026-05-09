# 架构说明：internal/api/wechat/handler.go

## 模块职责

处理微信公众号服务器回调：
1. 接入验证 (GET)
2. 消息接收和回复 (POST) — **集成大模型生成智能回复**
3. **用户自动创建** — 关注事件时创建用户，所有消息兜底确保用户存在

## 核心结构体

```go
// LLMClient 大模型客户端接口
type LLMClient interface {
    ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error)
}

// UserService 用户服务接口
type UserService interface {
    GetOrCreateByOpenID(openID string) (*user.User, bool, error)
}

// Handler 微信公众号处理器
type Handler struct {
    config      Config      // 微信配置 (Token, AppID, etc.)
    llmClient   LLMClient   // 大模型客户端（依赖接口而非具体实现）
    userService UserService // 用户服务（自动创建用户）
}
```

## 公开方法

| 方法 | 职责 |
|------|------|
| `NewHandler(config Config, llmClient LLMClient, userService UserService) *Handler` | 创建处理器（注入 LLM 客户端和用户服务） |
| `Verify(c *gin.Context)` | 处理 GET 接入验证 |
| `HandleMessage(c *gin.Context)` | 处理 POST 消息 |

## 签名验证流程

```
1. 获取 URL 参数: signature, timestamp, nonce, echostr
   ↓
2. 参数非空校验
   ↓
3. 将 token + timestamp + nonce 字典序排序
   ↓
4. 拼接后 SHA1 加密
   ↓
5. 对比加密结果和 signature
   ↓
6. 一致 → 返回 200 + echostr；不一致 → 返回 403
```

## 消息处理流程

```
1. 读取请求 Body
   ↓
2. 解析 XML 到 Message 结构体
   ↓
3. **获取或创建用户（兜底机制）**
   ├─ 调用 userService.GetOrCreateByOpenID
   ├─ 失败仅记录警告，不影响消息处理
   ↓
4. 根据消息类型分发
   ↓
4. 调用 callLLM() 生成智能回复
   ├─ 构造 messages: [system prompt, user content]
   ├─ 调用 llmClient.ChatCompletion()
   ├─ 成功 → 返回 LLM 响应
   └─ 失败 → 返回降级提示 + 记录警告日志
   ↓
5. buildResponse() 包装 XML 响应
   ↓
6. 设置 Content-Type: application/xml，返回响应
```

### 系统提示词

```go
const systemPrompt = "你是一个友好的智能客服助手，请用简洁的中文回应用户的问题。"
```

### callLLM 函数设计

统一封装所有消息类型的 LLM 调用：
- 自动添加系统提示词
- 按消息类型标记日志
- 记录调用耗时
- 错误降级处理

## 当前消息处理策略

**所有消息类型调用大模型生成智能回复**

| 消息类型 | Prompt 内容 | 说明 |
|----------|-------------|------|
| text (文本) | 用户消息原文 | 直接使用用户输入 |
| image (图片) | "用户发送了一张图片" | 提示 LLM 是图片消息 |
| voice (语音) | "用户发送了一条语音消息" | 提示 LLM 是语音消息 |
| video (视频) | "用户发送了一条视频消息" | 提示 LLM 是视频消息 |
| shortvideo (小视频) | "用户发送了一条小视频消息" | 小视频消息 |
| location (位置) | "用户发送了位置信息" | 位置消息 |
| link (链接) | "用户发送了一个链接" | 链接消息 |
| subscribe (订阅事件) | "用户刚刚关注了公众号，请生成友好的欢迎语" | 新用户订阅 + 显式创建用户记录 |
| CLICK (菜单点击) | "用户点击了菜单按钮：{EventKey}" | 菜单点击事件 |
| 未知类型 | "用户发送了未知类型的消息" | 兜底处理 |
| unsubscribe / VIEW | 不调用 LLM，不回复 | 取消订阅和跳转事件无需回复 |

## 错误处理与降级

**LLM 调用失败降级**：
```
LLM 调用返回 error
    ↓
logger.WarnWithFields 记录（包含 msg_type、error、duration）
    ↓
返回固定提示："服务暂时不可用，请稍后再试"
```

**UserService 失败降级**：
```
GetOrCreateByOpenID 返回 error
    ↓
logger.WarnWithFields 记录（包含 open_id、error）
    ↓
继续正常处理消息，不中断用户体验
```

## 回复格式

遵循微信要求的 XML 格式:
```xml
<xml>
  <ToUserName><![CDATA[openid]]></ToUserName>
  <FromUserName><![CDATA[gh_appid]]></FromUserName>
  <CreateTime>timestamp</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[LLM生成的智能回复]]></Content>
</xml>
```

> 注意: ToUserName = 用户 openid, FromUserName = 公众号 appid，需要交换发送方和接收方

## 可测试性设计

### MockLLMClient

单元测试使用 Mock 客户端：
```go
type MockLLMClient struct {
    returnString  string    // 配置返回值
    returnError   error     // 配置错误
    called        bool      // 记录是否被调用
    lastMessages  []llm.ChatMessage  // 记录调用参数
}
```

### MockUserService

单元测试使用 Mock 用户服务：
```go
type MockUserService struct {
    returnUser  *user.User // 配置返回用户
    returnIsNew bool       // 是否为新用户
    returnError error      // 配置错误
    called      bool       // 记录是否被调用
    lastOpenID  string     // 记录调用参数
}
```

## 路由集成

在 `routes.go` 中创建并注入 LLM 客户端：
```go
llmClient, err := llm.NewClient(cfg.LLM)
if err != nil {
    logger.Fatal("Failed to create LLM client", zap.Error(err))
}
wechatHandler := wechat.NewHandler(wechat.Config{...}, llmClient)
```

## 已实现能力

- ✅ 微信官方标准签名验证
- ✅ 完整 XML 消息结构体，支持所有消息类型
- ✅ GET 接入验证
- ✅ POST 消息接收解析
- ✅ LLM 客户端接口抽象与依赖注入
- ✅ 所有消息类型的智能回复生成
- ✅ 系统提示词自动添加
- ✅ 调用失败降级处理
- ✅ 完整的结构化日志记录
- ✅ 取消订阅/VIEW 事件保持不回复行为
- ✅ **UserService 接口抽象与依赖注入**
- ✅ **关注事件时自动创建用户**
- ✅ **所有消息兜底创建用户（确保用户数据存在）**
- ✅ **用户服务错误不影响消息处理**

## 配置依赖

需要配置:
- `wechat.token` 用于签名验证
- `llm.*` 大模型厂商配置（通义千问、字节豆包等）

## 测试

测试计划参见: `docs/testing/test-plans/internal/api/wechat/handler.md`

单元测试覆盖:
- ✅ 签名验证（有效/无效）
- ✅ 文本消息 + LLM 成功（验证 UserService 被调用）
- ✅ 文本消息 + LLM 失败降级
- ✅ 图片消息（验证 Prompt 正确构造）
- ✅ 订阅事件（验证欢迎语 Prompt + 用户创建）
- ✅ 订阅事件 UserService 错误不影响消息处理
- ✅ 取消订阅不回复
