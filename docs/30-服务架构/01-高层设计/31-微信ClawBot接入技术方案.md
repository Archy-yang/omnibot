# 31 - 微信 ClawBot 接入技术方案

| 项 | 内容 |
|----|------|
| 版本 | v2.8 |
| 状态 | ✅ 已实现(v2.8.0) |
| 关联 PRD | `docs/20-产品PRD/in_progress/v2.8-微信ClawBot接入PRD.md` |
| 前置 | v2.3 微信绑定（BindingService 通用化）、v1.6 飞书长连接通道 |
| 调研对象 | 腾讯官方 `@tencent-weixin/openclaw-weixin@2.4.10`（MIT，仓库 Tencent/openclaw-weixin） |

## 1. 结论先行

「微信 ClawBot」不是公众号开放平台，而是 **2026-03 微信官方给 OpenClaw 出的个人号插件**：

- 用户在微信里扫码授权后，微信侧出现一个「微信Claw」机器人身份；
- 机器人通过 **HTTP 长轮询**（不是 webhook，也不是 WebSocket）从微信后端取消息、回消息；
- 官方只提供了 OpenClaw（Node）插件形态，但**协议是公开的 MIT 源码 + 纯 HTTP/JSON**，
  所以我们可以用 Go 原生实现一套客户端，不部署 OpenClaw、不依赖 Node 运行时。

已实测确认（本机直连成功）：

```bash
curl "https://ilinkai.weixin.qq.com/ilink/bot/get_bot_qrcode?bot_type=3" \
  -H 'iLink-App-Id: bot' -H 'iLink-App-ClientVersion: 132106'
# {"qrcode":"1c30...","qrcode_img_content":"https://liteapp.weixin.qq.com/q/7GiQu1?qrcode=...&bot_type=3","ret":0}
```

`qrcode_img_content` 是一个 URL，把它渲染成二维码给用户扫即可（官方插件也是这么做的）。

## 2. 协议画像（从源码提取，非猜测）

### 2.1 公共约定

- Base URL：`https://ilinkai.weixin.qq.com`（登录成功后服务端可能下发 `baseurl`，需按账号存储）
- 请求头（所有请求）：

| Header | 值 | 说明 |
|---|---|---|
| `iLink-App-Id` | `bot` | 插件 package.json 的 `ilink_appid` |
| `iLink-App-ClientVersion` | `uint32 = major<<16 \| minor<<8 \| patch` | 2.4.10 → `132106` |
| `Content-Type` | `application/json` | POST |
| `AuthorizationType` | `ilink_bot_token` | 鉴权类型 |
| `Authorization` | `Bearer <bot_token>` | 登录后才有 |
| `X-WECHAT-UIN` | `base64(十进制随机 uint32)` | 每次请求重新生成 |

- 每个请求体额外带 `base_info: {channel_version, bot_agent}`（观测用，不参与鉴权）。

### 2.2 登录（扫码）

```
GET  /ilink/bot/get_bot_qrcode?bot_type=3        → { qrcode, qrcode_img_content, ret }
GET  /ilink/bot/get_qrcode_status?qrcode=&verify_code=   (长轮询，35s)
```

状态机（`status` 字段）：

| status | 处理 |
|---|---|
| `wait` | 继续轮询（1s 间隔） |
| `scaned` | 已扫码，等待确认；打印提示 |
| `need_verifycode` | 微信里显示数字，让用户输入 → 带 `verify_code` 重试 |
| `scaned_but_redirect` | 换 IDC：用 `redirect_host` 拼新 baseUrl 继续轮询 |
| `confirmed` | 成功：取 `bot_token` / `ilink_bot_id` / `ilink_user_id` / `baseurl`，落库 |
| `expired` | 二维码过期 → 重新 `get_bot_qrcode`（最多 N 次） |
| `verify_code_blocked` | 数字多次错误 → 刷新二维码 |
| `binded_redirect` | 该微信已绑过这个 bot → 提示「已连接过」 |

### 2.3 收消息（长轮询）

```
POST /ilink/bot/getupdates
{ "get_updates_buf": "<游标>", "base_info": {...} }

→ { ret, errcode, errmsg, msgs: [WeixinMessage...],
    get_updates_buf: "<新游标>", longpolling_timeout_ms }
```

- **游标必须持久化**：`get_updates_buf` 原样缓存，下次请求带回；丢了会重放历史消息。
- 客户端超时（35s 无响应）视为正常，直接重试；连续失败 3 次退避 30s。
- `errcode/ret = -14` 表示会话失效 → 停止轮询、标记账号失效、提示重新扫码。

消息结构（`WeixinMessage`）：

```jsonc
{
  "seq": 1, "message_id": "…", "from_user_id": "…", "to_user_id": "…",
  "client_id": "…", "create_time_ms": 0, "session_id": "…", "group_id": "…",
  "message_type": 1,            // 1=USER 2=BOT
  "message_state": 2,           // 0=NEW 1=GENERATING 2=FINISH
  "context_token": "…",         // 回消息必须带回（会话锚点）
  "item_list": [ { "type": 1, "text_item": { "text": "你好" } } ]
}
```

item 类型：1=text、2=image、3=voice、4=file、5=video、11/12=tool_call。**本期只处理 text**，
其余回固定提示「暂不支持这类消息」。

### 2.4 发消息

```
POST /ilink/bot/sendmessage
{ "msg": {
    "from_user_id": "", "to_user_id": "<对方 ilink user id>",
    "client_id": "openclaw-weixin-<随机>",
    "message_type": 2, "message_state": 2,
    "item_list": [{ "type": 1, "text_item": { "text": "回复内容" } }],
    "context_token": "<入站消息带来的 token>"
  }, "base_info": {...} }
→ { message_id, ret, errmsg }
```

- `context_token` 是会话锚点，**按对端缓存**（入站时写入，出站时取用）。
- 长文本需分段发送（微信单条有长度限制，官方插件按段落切分，每段一次请求）。

### 2.5 其他端点（低频）

| 端点 | 用途 | 本期 |
|---|---|---|
| `POST /ilink/bot/msg/notifystart` | 通道启动通知 | ✅ 启动时 |
| `POST /ilink/bot/msg/notifystop` | 通道停止通知 | ✅ 退出时 |
| `POST /ilink/bot/getconfig` | 取 `typing_ticket` | ⬜ 二期 |
| `POST /ilink/bot/sendtyping` | 「对方正在输入」 | ⬜ 二期 |
| `POST /ilink/bot/getuploadurl` | 媒体上传（AES-128-ECB + CDN） | ⬜ 二期 |

> 媒体链路还要处理 `novac2c.cdn.weixin.qq.com` 上传、AES-128-ECB、SILK→PCM 转码，
> 是独立的一大块，本期明确不做。

## 3. 架构设计

### 3.1 分层

```
internal/channel/wechatclaw/    协议层：纯 HTTP 客户端 + 长轮询 + MessageChannel 实现
internal/service/wechatclaw/    服务层：扫码登录状态机、账号凭证、绑定
internal/repository/wechatclaw/ 仓储：wechat_claw_accounts 表
internal/domain/wechatclaw/     领域实体（无外部依赖）
internal/channel/im/            【新增】渠道无关的入站 pipeline（飞书/ClawBot 共用）
internal/api/web/wechat_claw_handler.go  Web API：扫码、状态、断开
```

### 3.2 共享 IM pipeline（N1）

飞书 handler 的 `HandleInbound` 已经是「身份解析 → 绑定码 → 反馈指令 → 记忆命令 →
落库 → 上下文 → 前置汇报 → 选 LLM → Agent.Run → 落库 → 回复」的完整链路，且全部依赖走接口。
把它上移成 `internal/channel/im`：

```go
im.NewHandler(im.Options{
    ChannelType: "feishu",                    // 或 "wechat_claw"
    Source:      domainagent.SourceFeishu,    // 或 SourceWeChatClaw（新增）
    Sender:      sender,
    Binding:     bindingSvc, Messages: msgSvc, Agent: agentSvc, LLMConfig: llmConfigSvc,
})
```

- `feishu.NewMessageHandler(...)` 保留同名签名，内部返回 `*im.Handler`（薄壳），
  **现有飞书测试一行不改**即为重构回归证明。
- ClawBot 复用同一条链路，只在 `Sender`/`Source` 上不同。

### 3.3 数据模型

```go
// wechat_claw_accounts（单账号：全库同时只有一条 status=active）
type Account struct {
    ID          int64
    BotID       string    // ilink_bot_id
    ILinkUserID string    // ilink_user_id（机器人自己的身份）
    BaseURL     string    // 登录返回的 baseurl，缺省用固定域名
    TokenCipher string    // bot_token，AES-256-GCM 加密（pkg/crypto）
    SyncBuf     string    // get_updates_buf 游标（重启续传）
    Status      string    // active / expired
    CreatedAt, UpdatedAt time.Time
}
```

- **token 属敏感凭证**：加密入库、日志脱敏（只打前 6 位 + 长度），符合安全红线。
- 游标单列存储，避免重启重放历史消息。

### 3.4 身份与绑定（扫码即绑定）

- 通道类型：`wechat_claw`（与公众号 `wechat` 完全隔离）。
- **扫码即绑定**：ClawBot 的 Semantic 与公众号不同——扫码的人就是这台机器人的主人
  （消息的 `from_user_id` 与登录返回的 `ilink_user_id` 完全一致）。因此 confirmed 时直接用
  `BindingService.BindDirect("wechat_claw", 发起扫码的 web 用户, ilink_user_id)` 建立绑定，
  **不再要求用户在微信里发绑定码**。
- `wechat_claw_accounts.login_user_id` 记录扫码发起人；服务启动时若凭证在但绑定关系缺失，
  自动补绑（自愈）。
- 绑定失败不阻断连接（扫码本身已成功），下次启动重试。
- 非主人微信（理论上不会有）仍走「未绑定」引导，不建号、不进对话。
- v2.3 通用绑定码机制保留给其它渠道（飞书 / 公众号），ClawBot 不再使用。

### 3.5 生命周期

```
Web 扫码确认 ──► AccountService.Save(token 加密) ──► Monitor.Start()
                                                        │
                            getupdates 循环 ◄───────────┘
                                    │ 入站 text
                                    ▼
                        im.Handler.HandleInbound
                                    │
                          Sender.SendText(to_user_id, text, ctxToken)
```

- 服务启动时若存在 `status=active` 账号 → 自动拉起 monitor（`backtask.RunDetached`）。
- 扫码成功后无需重启：登录服务直接启动 monitor。
- 断开：停 monitor → `notifystop` → 删账号。

## 4. TDD 开发顺序

| 步骤 | 内容 | 测试 |
|---|---|---|
| N1 | 抽 `internal/channel/im`，飞书改薄壳 | 飞书现有测试全绿（零改动） |
| N2 | `wechatclaw` 协议层：headers / 请求体 / 端点 / 响应解析 | `httptest` 断言方法与 body |
| N3 | 登录状态机：wait/scaned/confirmed/expired/redirect/-14 | 表驱动单测 |
| N4 | 凭证存储：加密/解密、单账号约束、游标续传 | repo + service 单测 |
| N5 | monitor：首轮拉取、游标推进、失败退避、-14 停摆 | fake client 单测 |
| N6 | 发送通道：`MessageChannel` 实现 + context_token 缓存 | mock client 单测 |
| N7 | Web API：扫码/状态/断开 + 鉴权 | handler 单测（httptest） |
| N8 | 前端：设置页「微信 ClawBot」扫码区块 | `npm run build` |
| N9 | 文档 + CHANGELOG + 部署验证 | 手工端到端（真机扫码） |

## 5. 风险与红线

1. **个人号自动化是 Public Beta**：官方明确要求遵守微信生态内容合规。本方案只做
   「用户与自己机器人单聊」场景，不群发、不加群、不主动加好友；README/设置页需带提示。
2. **协议非正式开放 API**：官方随时可能变更（版本头、端点、返回结构）。对策：
   版本号/域名走配置、错误码集中映射、协议层单测覆盖，改版时只动一个包。
3. **token 泄漏 = 账号被接管**：加密存储 + 日志脱敏 + 不通过 API 回传明文。
4. **长轮询常驻连接**：容器需出网到 `ilinkai.weixin.qq.com`；断网 3 次退避，不刷屏日志。
5. **游标丢失会重放**：`SyncBuf` 落库并在每轮更新，重启续传。
6. **不自动建号**：沿用 v2.3 绑定模型，未绑用户只回引导。

## 6. 不做（本期）

- 图片/语音/文件/视频收发（CDN + AES-128-ECB + SILK 转码）
- 「正在输入」状态（getconfig + sendtyping）
- 多账号并发、群聊、@ 解析
- 引用回复（quote-store）、流式分段渲染

## 7. 落地记录

实现与偏差记录（2026-10-30）：

| 项 | 结果 |
|---|---|
| 共享 pipeline | `internal/channel/im`（`Handler.HandleInbound` + 记忆命令 + 反馈指令 + 绑定文案参数化）；飞书 `NewMessageHandler` 变薄壳，原测试仅改方法名引用（`HandleMemoryCommand`/`HandleNoteFeedback`），逻辑断言全绿 |
| 协议层 | `internal/channel/wechatclaw`：`client.go`（headers/端点/错误码）、`types.go`（FlexString 保 uint64 精度）、`sender.go`（1800 rune 分段） |
| 登录 | `internal/service/wechatclaw/service.go`：`StartLogin`/`PollLogin`/`Status`/`Disconnect`/`Start`；5 分钟会话 TTL；confirmed 后落库 + 立即拉起长轮询 |
| 凭证 | `wechat_claw_accounts` 单账号表；`crypto.Encrypt` 落密文；`Disconnect` 清空；`Start` 校验 active 后恢复 |
| 长轮询 | `monitor.go`：3 次失败退避 30s；`errcode -14` 标记 expired 并停止；游标每轮 `UpdateSyncBuf` |
| 发送 | `channel.go` 实现 `MessageChannel`（异步、provider 动态取 sender）；已注册 `channelfactory`，主动推送可复用 |
| Web API | `internal/api/web/wechat_claw_handler.go`：login(起扫码) / login/:qrcode(轮询) / status / DELETE；`ErrQRExpired → 410 qrcode_expired` |
| 前端 | `services/wechatClaw.ts` + 设置抽屉「微信接入」section（qrcode 本地渲染、1.5s 轮询、数字校验、断开）；「渠道绑定」加第三行 |
| 数据库 | `AutoMigrate` 加 `wechatclaw.Account` |

**与方案的偏差**：

1. `SendMarkdown`/`SendCard` 降级为纯文本（ClawBot 不渲染 markdown），未单独做卡片形态。
2. 单条消息**串行**处理（与官方插件一致），长任务会阻塞下一轮拉取——二期可改带并发信号量。
3. `typing_ticket`（sendtyping）与媒体 CDN 未接入，按方案 §6 留二期。
4. 主动管家推送走 `channelfactory` 已注册的 `wechat_claw` 通道（但 `user_channels` 需先完成绑定码绑定）。
