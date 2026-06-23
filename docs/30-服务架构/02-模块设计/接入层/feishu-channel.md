# 飞书机器人通道

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.6+ |
| 最后更新 | 2026-06-22 |
| 状态 | ✅ 已实现 |

---

## 1. 这个文档解决什么问题?

说明飞书机器人通道(`internal/channel/feishu/`)的实现细节、长连接接收模型、与
Web/微信通道的差异,以及 v1.6 关键架构决策「同步 Run 是 RunStream 之上的聚合层」对
本通道的意义。

读之前你需要知道:
- [统一消息通道接口](./message-channel.md)
- [Web 对话通道](./web-channel.md)
- [微信通道](./wechat-channel.md)
- [智能体服务](../智能体层/agent-service.md)
- 产品 PRD: `docs/20-产品PRD/in_progress/v1.6-飞书机器人接入PRD.md`

---

## 2. 飞书通道核心特性

### 与现有通道对比

| 特性 | 微信通道 | Web 通道 | 飞书通道 |
|------|---------|---------|---------|
| 接收模型 | HTTP 回调 | HTTP / SSE | **长连接 WebSocket** |
| 公网依赖 | 需公网回调 | 需公网访问 | **无需公网回调** |
| 同步响应 | ✅ 5 秒限制 | ✅ 无限制 | ✅ IM 异步无限制 |
| 流式输出 | ❌ | ✅ SSE | ❌(IM 不支持) |
| 思考过程展示 | ❌ | ✅ segments 交错 | ❌(仅最终文本) |
| Agent 能力 | ❌(超时限制) | ✅ 流式 + 同步 | ✅ **同步 Run** |
| 富媒体 | ❌ 纯文本 | ✅ Markdown | ❌ v1.6 仅文本 |
| 用户识别 | OpenID + wechat_accounts | session_id + user_channels | open_id + user_channels |
| 消息去重 | 微信 MsgID | 不需要 | **飞书 message_id** |
| 异步发送 | ❌ | ✅ | ✅ |

### 接入模式选择:为什么长连接

飞书提供两种接收模式:**事件订阅(HTTP 回调)** 与 **长连接(WebSocket)**。本版选定
长连接,原因:

- OmniBot 当前单机/内网部署,无公网回调地址
- 飞书官方 SDK `github.com/larksuite/oapi-sdk-go/v3` 内置长连接客户端 + 自动重连
- 本地即可联调,无需内网穿透
- 事件订阅模式留作未来生产部署的可选增强

---

## 3. 架构与代码分层

```
internal/channel/feishu/
  interfaces.go    — 全部依赖走接口(UserService / MessageService / AgentService /
                     LLMConfigService / Sender),handler 完全可单测
  handler.go       — MessageHandler.HandleInbound: 纯逻辑 pipeline,无 SDK 依赖
  channel.go       — Channel: 实现 MessageChannel + Start(SDK 长连接 goroutine);
                     dispatchInbound 把 SDK 事件翻译为中性 InboundMessage
  sender.go        — larkSender: 飞书 SDK 发文本消息(JSON `{"text":"..."}`)
```

**可测性核心**:`HandleInbound` 不碰 SDK,SDK 事件解析在 `dispatchInbound` 内完成翻译。
单测 mock 全部接口即可验证 pipeline。`Channel.Start` 通过注入 `Starter` 接口隔离真实
WebSocket 连接,可单测启动开关行为。

---

## 4. 消息处理 Pipeline(`HandleInbound`)

```
飞书 IM 收到消息事件
        │
        ▼
SDK dispatcher 回调 → dispatchInbound 解析 → InboundMessage{OpenID,Text,MsgID,ChatType}
        │
        ▼
ChatType != "p2p" ? → 忽略(v1.6 仅单聊)
Text == "" ?       → 忽略
        │
        ▼
GetOrCreateByChannel("feishu", OpenID) → user_id  ← 复用通用渠道接口
        │
        ▼
SaveUserMessage(ctx, userID, text, MsgID)  ← 飞书 message_id 做去重
        │  ↓ 若 ErrDuplicateMessage:静默丢弃,不再回复(SDK 可能因网络重投)
        │
        ▼
BuildContextMessages(ctx, userID, text)  ← 滑动窗口(最近 10 轮) + 长期记忆
        │
        ▼
选 LLM:用户自定义配置优先,否则 default(与 Web 同步端点完全一致)
        │
        ▼
AgentService.Run(ctx, userID, msgs, activeLLMClient) → AgentResult{FinalResponse, Records}
        │   ↑ 同步 Run 是 RunStream 之上的聚合层(v1.6 架构核心)
        │
        ▼
records → []*conversation.AgentStep(同款转换 helper,与 web 同步端点对齐)
        │
        ▼
SaveAssistantMessageWithSegments(ctx, userID, FinalResponse, nil, steps)
        │   ↑ segments=nil(IM 入口无交错段);steps 落 agent_steps 表
        │
        ▼
Sender.SendText(ctx, OpenID, FinalResponse)  ← 飞书 API 回复
```

**错误兜底**:
- Agent 执行失败 → 给用户回 `服务暂时不可用,请稍后再试`,不落 assistant 消息
- Sender 失败 → 仅记日志,不阻断主流程(消息已落库)
- 单条消息 panic → recover 兜底,不让长连接断

---

## 5. v1.6 架构核心:同步 Run = RunStream 聚合

飞书通道接入推动了一个更深的架构定义:**底层只剩一个真实流式实现,同步 `Run` 是
`RunStream` 之上的一层聚合封装**。流式/同步只是返回形式不同——存储与运行链路记录
完全一致,记录逻辑只在 `RunStream` 单一来源。

具体到本通道:
- `AgentService.Run` 内部调 `RunStream` drain 事件 channel
- Token 事件 → 拼成 `FinalResponse`(送飞书的最终文本)
- LLMCall / ToolResult 事件 → 折叠为 `[]StepRecord`(聚合产出,handler 转 AgentStep 落库)
- 飞书 handler 与 Web 同步端点共享同款 `recordsToAgentSteps` 转换逻辑

这意味着:
- 飞书端 agent_steps 落库行为与 Web 流式端点对齐——同一条 SQL 即可还原飞书侧任一轮
  对话的完整 ReAct 链(llm_call → tool_call → llm_call → ...)
- 未来增加第 4 个入口时,只要走 `AgentService.Run`,自动获得完整记录能力

---

## 6. 配置

`pkg/config/config.go` 新增 `FeishuConfig`,通过 viper mapstructure 自动加载:

```yaml
feishu:
  app_id: "cli_xxx"        # 飞书自建应用 App ID
  app_secret: "xxx"        # 飞书自建应用 App Secret
  enabled: true            # false 时跳过初始化,不影响其他通道
```

`enabled=false` 是开发态友好的关键:dev 环境未配置飞书凭证时,服务正常启动,不抛错。

**安全**:凭证仅 config.yaml 内存,不入库不日志(安全红线)。`configs/config.yaml`
在 .gitignore 内,不入仓库。

---

## 7. SDK 依赖

- `github.com/larksuite/oapi-sdk-go/v3` v3.9.6 — 飞书官方 Go SDK
- `github.com/gorilla/websocket` v1.5.0 — 上面 SDK 长连接传递依赖

关键 API:
- `lark.NewClient(appID, appSecret)` — 主 client(发消息)
- `larkws.NewClient(appID, appSecret, larkws.WithEventHandler(dispatcher))` — 长连接 client
- `wsClient.Start(ctx)` — 阻塞启动长连接,SDK 自带心跳/重连(2 分钟 ping,30 秒重试)
- `dispatcher.NewEventDispatcher("", "").OnP2MessageReceiveV1(handler)` — 注册消息事件
- `client.Im.V1.Message.Create(ctx, *CreateMessageReq)` — 发文本消息

长连接模式下 dispatcher 的 verificationToken/eventEncryptKey 留空,SDK 用 app
credentials 验证。

---

## 8. 启动流程

`routes.go::SetupRouter` 末尾调 `startFeishuChannel`:

1. `cfg.Feishu.Enabled=false` → return,什么都不做
2. 凭证空但 enabled=true → 警告日志,return(不阻断主服务)
3. 否则:
   - 构造 `lark.NewClient` + `larkSender`
   - 构造 `MessageHandler`(注入 userSvc/msgSvc/agentSvc/llmConfigSvc + sender)
   - 构造 `Channel`(注入 handler/sender/默认 ws Starter)
   - `channelfactory.Register(channel)` — 加入全局通道注册表
   - `go channel.Start(ctx)` — 后台 goroutine 启动长连接,带 recover

---

## 9. v1.6 范围说明

**已做**:
- 单聊文本接收 + Agent 回复
- agent_steps 完整落库(与 Web 端对齐)
- 跨入口能力打通(记忆/LLM 配置)
- enabled 开关 + 凭证空校验

**未做(留后续)**:
- 群聊 @ 路由(`ChatType=="group"` 当前忽略)
- 富消息卡片(交互式 / 图片 / 文件 / 语音)
- 思考过程在飞书端的可视化展示
- 事件订阅 HTTP 回调模式(留生产部署需要时再补)
- v1.8 `UserChannels` 重构时,wechat_accounts 并入 user_channels(目前飞书已直接走 user_channels)

---

## 10. 测试覆盖

- `handler_test.go` 8 个用例:p2p 正常 / 群聊忽略 / 重复消息 / 自定义 LLM / Step Model 注入 /
  Agent 错误兜底 / Sender 错误不崩 / 空文本跳过
- `channel_test.go`:Start 开关四种状态 / extract content / dispatchInbound 翻译层四种事件 /
  SendText 转发 / ChannelType
- 真实飞书 smoke:见 `docs/50-测试/test-plans/v1.6-feishu-bot.md`
