# 变更日志

本文件记录项目所有重要版本的变更内容。

---

## [v1.6.0] - 2026-06-22

### 🚀 用户可见

- **第三个入口:飞书机器人接入**——飞书 IM 用户可与 OmniBot 单聊,直接享有 v1.5 全部
  Agent 能力(工具调用 / 多步推理)、长期记忆、用户自定义 LLM 配置。
- 飞书端通过**长连接(WebSocket)**接收消息——开发/内网部署无需公网回调地址。
- 跨入口能力打通:同一用户在 Web 与飞书侧共享 user_id,长期记忆与 LLM 配置完全一致。

### 🔧 架构核心(本版关键)

- **底层只剩一个真实流式实现,同步 Run 是 RunStream 之上的聚合层**。流式/同步只是
  返回形式不同——存储与运行链路记录完全一致,**记录逻辑只在 RunStream 单一来源**,
  同步入口天然继承,杜绝两条路径行为漂移。
- **agent_steps 现在两条入口都落库**——同步 `Run` 通过聚合产出 `Records`,handler 转
  `conversation.AgentStep` 走和流式 handler 同一个 `SaveAssistantMessageWithSegments`。
  Web 同步端点 `POST /api/v1/chat/messages/agent` 也受益,与流式端点行为对齐。
- 新增 `agent.StepRecord` 中性记录结构 + `AgentResult.Records` 字段(`Steps` 旧字段
  保留兼容 `ReActAgent.Run` 的既有测试,无新调用方)。

### 🧩 实现要点

- **agent 包**:`service.go` 重写 `Run` = `RunStream` drain 事件聚合
  (Token→FinalResponse,LLMCall/ToolResult→StepRecord),抽 `runStreamWithClient`
  私有助手共享;variadic `customLLMClient` 做 `StreamingLLMClient` 类型断言适配,
  失败静默回退 default。
- **`internal/channel/feishu`** 新增包:
  - `interfaces.go`:UserService / MessageService / AgentService / LLMConfigService /
    Sender 全部接口抽象,handler 完全可单测
  - `handler.go` `MessageHandler.HandleInbound`:纯逻辑 pipeline,镜像 web 同步端点
    (GetOrCreateByChannel("feishu") → SaveUserMessage 去重 → BuildContext → 选 LLM →
    Run → Records→steps 落库 → Sender.SendText)
  - `channel.go` `Channel`:实现 `MessageChannel` 接口 + `Start(ctx)` 阻塞长连接 +
    `Starter` 抽象隔离 SDK(测试 mock)
  - `sender.go` `larkSender`:绑飞书 SDK 发文本,JSON `{"text":"..."}`
- **routes.go**:enabled 时构造 channel,`go channel.Start(ctx)` 带 recover;
  enabled=false 静默跳过(开发态友好)
- **pkg/config**:新增 `FeishuConfig{AppID, AppSecret, Enabled}`

### 🔐 安全

- 飞书凭证仅 `config.yaml` 内存,不入库不日志
- 工具失败脱敏共用 v1.5.3 `sanitizeToolResult`,飞书端回复也不含原始错误细节
- agent_steps 记录飞书端原始未脱敏 raw_result(内部表,与展示分离)

### ⚠️ 兼容性 / 范围

- **不动**:`RunStream` 行为 / v1.5 工具/消息/记忆/LLM 配置 / 微信端 / Web 流式端点 /
  DB 表结构(user_channels 已支持任意 channel_type,零迁移)
- 飞书端仅文本单聊;群聊、富消息卡片、图片/文件/语音、思考过程展示留后续
- 事件订阅 HTTP 回调模式不做(仅长连接)

### 📚 文档

- 新增 `docs/20-产品PRD/in_progress/v1.6-飞书机器人接入PRD.md`
- 新增 `docs/30-服务架构/02-模块设计/智能体层/feishu-channel.md`
- 新增 `docs/50-测试/test-plans/v1.6-feishu-bot.md`
- 演进路线图 v1.6 小节注记:选定飞书 + 长连接 + Run/RunStream 统一架构定义

### 📦 依赖

- `github.com/larksuite/oapi-sdk-go/v3` v3.9.6(飞书官方 Go SDK)
- `github.com/gorilla/websocket` v1.5.0(SDK 长连接传递依赖)

---

## [v1.5.5] - 2026-06-21

### 🔧 架构改进

- **Agent 运行链路记录（实体层首次落地）**：新增 `agent_steps` 独立表，把一轮对话的
  **完整执行链**按顺序记下来——不只工具调用，连**每次 LLM 调用**（发出的 messages、模型
  回复/决定调的工具、耗时、模型名）也记。一轮 ReAct 循环由有序步骤组成
  （llm_call / tool_call），靠 `message_id + seq` 一句 SQL 还原完整时序。这是
  「JSON 骨架 + 实体表」存储架构中实体层的首次落地，供复盘与将来分析。
- **为什么记整条链而非只记工具**：只记工具调用是残缺的——看不到「模型为什么这么走、
  发了什么 prompt、每步多久」。按模型调用链依次记录才能完整复盘一轮对话。
- **运行时与记录两条路分离**：运行时上下文仍由滑动窗口（最近10轮、纯文本）框住，工具结果
  是「轮内消耗品」，跨轮只留最终文本结论；`agent_steps` 是**纯离线**记录，不进运行时上下文、
  不影响模型看到什么、不影响上下文成本。因此记录表按「分析最好查」设计，放开手记全。
- **展示与记录职责分离**：`messages.segments` 的 `result` 维持**脱敏展示**（失败显
  「工具执行失败」），`agent_steps` 的 tool 步骤 `response` 存**完整原始**结果（含真实错误）。

### 🔐 安全

- 对外展示（segment.result、SSE tool_result）仍脱敏，原始错误不泄露给用户
- `agent_steps` 是内部记录/分析表，不对外暴露

### 🧩 实现要点

- `AgentEvent` 新增 `AgentEventLLMCall` 事件 + 通用 `StepStatus`/`StepDurationMs` +
  `LLMRequest`/`LLMResponse`；`RunStream` 每轮快照 messages、计时，轮末 emit llm_call
- `handler` 按事件时序累积 `[]AgentStep`（llm_call + tool_call，带 seq），流结束批量落库
- `SaveAssistantMessageWithSegments` 第 5 参数改为 `[]*AgentStep`，保存消息后 stamp
  `MessageID` 批量写；落库失败仅记日志，不影响主消息持久化（非原子）
- `AgentStepRepository` 经 `NewMessageService` 的 variadic 注入，零签名破坏

### 📋 数据模型

- `agent_steps`：`user_id` / `message_id` / `seq` / `kind`(llm_call|tool_call) / `status` /
  `duration_ms` / `tool` / `model` / `request` / `response` / `prompt_tokens` /
  `completion_tokens`(预留，本轮恒 0) / `created_at`

### ⚠️ 兼容性 / 范围

- 前端无改动（内部表，不展示）；仅 Web Agent 流式端点产生记录；AutoMigrate 自动建表
- token 用量本轮留列不填（将来加 `stream_options.include_usage`）；request 按完整存
  （离线线性追加可接受，将来涨爆再优化成增量/引用）

### 📚 文档

- 演进路线图 v1.5.5 小节：实体层首次落地 = Agent 运行链路 trace；「运行时瘦、记录全」结论
- `agent-service.md` 用 agent_steps 替换说明，画出步骤链表结构与「记录离线、不进上下文」关系

---

## [v1.5.4] - 2026-06-21

### 🚀 体验改进

- **思考过程历史持久化**：v1.5.3 的交错思考过程此前只活在前端内存，刷新页面后回退成
  纯文本。现在 Agent 流式回复的 segments（文本段 + 工具调用 + 脱敏后的工具结果）会随
  消息一起落库，**刷新页面后历史里仍能完整还原思考过程**，并可点击展开看工具结果。

### 🔧 架构改进

- **确立「JSON 骨架 + 实体表」混合存储方向**：经评估同类产品的内容格式后确定——纯展示
  碎片（文本、工具条、思考链、引用等）走消息内的 JSON 段落序列；未来的一等实体
  （artifact、生成文件、异步任务）走独立表 + 段落里放引用 id。本次只做 JSON 列，是该
  架构的第一步，未来扩展新内容类型零迁移。详见演进路线图 v1.5.4 小节。
- `messages` 表新增 `segments` JSON 列（GORM `serializer:json`，SQLite/PostgreSQL 通用），
  `content` 保留为纯文本投影（复制 / 上下文 / 搜索）。AutoMigrate 自动加列，无需数据迁移。
- domain 新增 `MessageSegment` 类型 + `NewAssistantMessageWithSegments` 构造函数
- service 新增 `SaveAssistantMessageWithSegments`，原 `SaveAssistantMessage` 保留
- `MessageDTO` 加 `segments` 字段（omitempty），`HandleGetHistory` 透传

### 🔐 安全

- 落库的工具结果与 SSE 推送**共用同一 `sanitizeToolResult` 脱敏**：工具失败结果存的是
  「工具执行失败」，原始 error（IP / 连接错误 / 堆栈）不写进 DB（安全红线）

### ⚠️ 兼容性 / 范围

- 仅 Web Agent 流式端点落库 segments；其余 3 个保存路径（同步、普通流式、Agent 同步）
  保持原样。旧消息 / 非 agent 消息 `segments` 为空，前端走纯文本回退渲染，无需迁移。
- 前端零改动：v1.5.3 的「有 segments 交错渲染、无则回退 content」逻辑天然兼容历史数据。
- `expanded` 是纯 UI 态，不持久化。

### 📚 文档

- 演进路线图 v1.5 章节追加 v1.5.4 小节，记录混合存储架构方向
- `agent-service.md` 补充 segments 持久化说明

---

## [v1.5.3] - 2026-06-21

### 🚀 体验改进

- **思考过程按真实时序交错展示**：Agent 回复若是「文本 → 调工具 → 文本」，前端现在
  按实际发生顺序交错渲染——先一段文本，再一条思考条，再思考之后的文本。此前所有
  文本被拼成一坨、所有工具堆在顶部，丢失了 LLM 的真实输出时序。
- **点击思考条展开看工具结果**：每条思考条可点击展开，查看该工具的返回结果（如时间、
  计算值、RSS 内容）。展开区限高 240px 滚动，长结果（RSS 全文 / 长 JSON）内部滚动，
  不撑乱对话。工具结果回来前思考条显示「正在调用 xxx…」旋转态。

### 🔐 安全

- **工具错误结果脱敏**：工具执行成功的结果原样展示（用户自己的查询），但执行失败的
  结果可能含内部细节（IP、连接错误、堆栈），统一脱敏为「工具执行失败」，不透传原始
  error（安全红线：错误不泄露内部实现）。见 `sanitizeToolResult`（handler.go）。

### 🔧 架构改进

- 后端放开此前被丢弃的 `AgentEventToolResult`，以 `event: tool_result` 推送给前端
  （脱敏后），不计入落库的 assistant 内容
- 前端消息模型从「content 一坨 + toolCalls 一个数组」改为有序 `segments` 段落序列
  （text / tool 交错），store 按 SSE 事件顺序维护

### 📋 SSE 协议变更

- 新增 `event: tool_result`、`data: {"tool": "...", "result": "..."}` —— 工具结果
  （紧跟对应 tool_call，错误已脱敏）
- 事件严格按 LLM 真实时序推送，前端据此交错渲染

### 🚮 删除内容

- 前端 `ChatMessage.vue` 的顶部堆叠式 `tool-call-strip` 状态条移除，换成交错的
  `tool-segment` 思考条
- 前端类型 `ToolCall` / `Message.toolCalls` 替换为 `MessageSegment` / `Message.segments`

### ⚠️ 兼容性

- segments 仅活在本次会话内存中，不做历史/DB 持久化；刷新页面后历史消息回退用
  `content` 纯文本渲染（思考过程丢失，符合预期）。历史 segments 持久化作为独立迭代后续做。

### 📚 文档

- `docs/30-服务架构/02-模块设计/智能体层/agent-service.md` 第 5 节更新 tool_result 协议
- `docs/30-服务架构/01-高层设计/02-演进路线图-v3.0.md` v1.5 章节追加 v1.5.3 小节

---

## [v1.5.2] - 2026-06-17

### 🚀 体验改进

- **Agent 真流式**：`/messages/agent/stream` 端点重构为 token 级流式输出，简单提问的
  首字延迟从原来「转圈 N 秒后整段吐」变为字符级实时渲染，体验等同 v1.5.0 之前的
  普通流式。工具调用问题也是在工具被决定调用的瞬间立即推送状态条，再继续流式 token。
- **默认全 Agent**：取消前端「思考模式」开关。所有对话默认走 Agent 路径，是否调用
  工具由 LLM 自动判断，符合《单一长期对话模型》决策。
- **工具状态条重设计**：把折叠面板「查看思考过程（N 步）」换成行内简洁状态条，
  显示在助手回复正文上方。无工具调用时不渲染状态条，简单聊天视觉等同纯文本，
  避免无关 UI 噪音。

### 🧰 工具友好化

- `Tool` 结构新增 `DisplayLabel` 字段，5 个内置工具补上中文文案：
  - `get_current_time` → 「查询了当前时间」
  - `calculator` → 「计算了一下」
  - `search_memories` → 「翻了翻记忆」
  - `search_history` → 「搜索了历史对话」
  - `rss_reader` → 「读取了 RSS 订阅」

### 🔧 架构改进

- 新增 `StreamingLLMClient` 接口（`ChatCompletionStream`），与同步 `LLMClient` 并存，
  上层按需选择
- 新增 `LLMStreamChunk` / `ToolCallDelta` / `AgentEvent` 流式类型层，明确区分 LLM
  原始增量和 Agent 高层事件
- `OpenAILLMClient` 实现 SSE 流式解析，支持 token delta 和 tool_call delta 跨 chunk
  累积，处理 [DONE] / finish_reason / API error 边界
- `ReActAgent.RunStream` 实现流式 ReAct 循环：边收 token 边转发，工具调用按 index
  累积参数后执行，结果塞回 messages 进入下一轮
- `HandleSendMessageAgentStream` 改为消费 `AgentEvent` channel，按 `event: token` /
  `event: tool_call` / `event: error` SSE 协议推给前端

### 📋 SSE 协议变更

- 旧协议（v1.5.0）：`event: agent_step` 一次性推送已完成的步骤摘要
- 新协议（v1.5.2）：
  - `event: token`、`data: {"content": "..."}` —— LLM token 增量
  - `event: tool_call`、`data: {"tool": "...", "label": "..."}` —— 工具调用开始
  - `event: error`、`data: {"error": "..."}` —— 错误
  - `data: [DONE]` —— 完成
- 旧 `event: agent_step` 协议同时下线（前端不再监听，后端不再产出）

### 🚮 删除内容

- 前端 `ChatInput.vue` 的「+」按钮、思考模式 popover 菜单、「思考」标签、
  `chat-thinking-mode` localStorage 持久化逻辑全部移除
- 前端类型 `AgentStep` / `AgentStepEvent` / `Message.agentSteps` 替换为
  `ToolCall` / `ToolCallEvent` / `Message.toolCalls`
- `chatService.sendMessageStream` 的 `isAgentMode` 参数删除，永远走 agent 流式路径

### ⚠️ 兼容性

- 后端 `/messages/stream` 普通流式端点保留为兼容兜底，前端不再调用，等 v2.0 全量
  验证 Agent 真流式稳定后再考虑彻底删除
- 微信端本次未改造为 Agent 路径（涉及客服消息异步推送），保持原行为；将作为
  独立小迭代后续完成

### 📚 文档

- `docs/30-服务架构/02-模块设计/智能体层/agent-service.md` 更新流式接口签名
- `docs/30-服务架构/01-高层设计/02-演进路线图-v3.0.md` v1.5 章节追加 v1.5.2 小节
- `docs/20-产品PRD/in_progress/v1.5.1-Agent模式可切换PRD.md` 移到 `completed/`
- `docs/20-产品PRD/in_progress/v1.5.2-Agent真流式与默认开启PRD.md` 移到 `completed/`

---

## [v1.5.1] - 2026-06-16

### ✨ 新增功能

- **Agent 模式可切换**：前端聊天输入框新增「思考模式」开关，用户手动决定单次对话
  走普通流式还是 Agent 流式，开关状态本地持久化到 `localStorage`

### 🔧 架构改进

- Agent 接口适配用户自定义 LLM 配置：和普通聊天逻辑保持完全对齐——优先用户自定义
  服务商/Model/API Key/BaseURL，缺省回落系统默认
- 修复 v1.5.0 中 Agent 服务硬编码使用全局 LLM 配置导致用户自定义配置不生效的问题

### 🚮 已废弃（v1.5.2 中移除）

本版本引入的「思考模式」开关在 v1.5.2 重新评估后被移除：单一长期对话模型不应该
有「模式切换」概念，是否调用工具应由 LLM 自动判断。详见 `docs/10-宪章/单一长期对话模型.md`。

---

## [v1.5.0] - 2026-06-15

### ✨ 新增功能

- **Agent 基本能力**
  - 新增 Tool Registry，支持工具注册、查询和 OpenAI Function Calling `tools` 格式转换
  - 新增 ReAct Agent 循环，支持 LLM → 工具调用 → 工具结果回传 → 继续推理
  - Web 对话新增 Agent SSE 流式端点，支持 `agent_step` 事件展示工具调用过程
  - 前端聊天消息支持展示「思考过程」折叠区

### 🧰 内置工具

- `get_current_time`：获取当前时间
- `calculator`：安全四则运算计算器
- `search_memories`：搜索用户长期记忆
- `search_history`：历史搜索占位工具，后续版本完善

### 🔧 架构改进

- 新增 `internal/service/agent` 包，Agent 层位于 Web Handler 与 LLM Client 之间
- 保持现有 `LLMProvider` 接口不变，Agent 通过 OpenAI-compatible adapter 支持 tools 参数
- Agent 默认最大步数 10，默认超时 120 秒，避免无限循环

---

## [v1.4.1] - 2026-06-14

### ✨ 新增功能

- **OpenAI 兼容服务商预设配置**
  - Web 设置面板新增 OpenAI 兼容服务商预设：OpenAI 官方、百度千帆、字节火山、阿里千问、自定义 OpenAI-compatible
  - 预设自动带出默认 Base URL 和推荐模型，用户仍可手动覆盖
  - 专用接口保留展示并标注"暂不可用"
  - 用户保存后的新预设统一走 OpenAI-compatible 调用路由

### 🔧 架构改进

- 新增后端 provider preset registry，前端通过 API 获取服务商选项，减少前后端硬编码不一致
- 用户级新 provider preset ID 统一路由到 `OpenAIProvider`
- 保留 legacy `qwen` / `doubao` 用户配置读取和调用兼容

### 🔐 安全

- API Key 继续 AES-256-GCM 加密存储，接口只返回脱敏信息
- 配置错误提示不暴露 API Key、内部堆栈或完整用户消息

---

## [v1.3.0] - 2026-06-13

### ✨ 新增功能

- **单条记忆删除与编辑**
  - 微信命令 `#删除记忆 N` 按序号删除单条记忆
  - Web API `DELETE /api/v1/memories/:id` 和 `PUT /api/v1/memories/:id`
  - Web 端记忆管理 UI：每条记忆支持删除按钮（确认弹窗）和内联编辑
  - 输入校验：空内容提示、200 字上限

- **SSE 流式对话输出**
  - 新增 `POST /api/v1/chat/messages/stream` SSE 端点
  - LLM Client 接口新增 `StreamChatCompletion`，OpenAI Provider 实现
  - 前端 `sendMessageStream` 使用 fetch + ReadableStream 逐 chunk 渲染
  - 打字机效果，用户无需等待完整响应
  - 同步端点保留，提供降级能力

### 🔧 架构改进

- **Chat 层窄接口 `LongTermMemoryProvider`**：chat 层只依赖 `GetRecentForContext`，不再依赖完整 `MemoryService`
- **Graphify 生成物清理**：`.gitignore` 新增 `graphify-out/`，已跟踪文件从索引移除
- **CLAUDE.md 路径修复**：演进路线图文档路径更新为重组后位置

### 📚 文档同步

- PRD、设计文档、系统架构总览、消息服务文档、微信通道文档全面更新
- 新增 `docs/90-迭代记录/archived/v1.3-长期记忆增强-SSE流式.md` 功能总结

### 🧪 测试

- 新增 29 个测试（21 个记忆删除/编辑 + 8 个 SSE 流式）
- `go test ./...` 全部通过
- `npm run build` 构建成功

---

## [v1.2.0] - 2026-05-12

### ✨ 新增功能

- **对话上下文记忆**
  - 机器人自动记住每个用户最近 10 轮对话内容
  - 用户可以使用「这个」「刚才的」「改成」等指代性词语进行连贯交流
  - 所有对话消息永久落库保存（用户消息 + AI 回复）
  - 调用 LLM 时自动拼接最近 10 轮历史消息作为上下文
  - 每次调用前重新插入 system prompt，保证人设一致性

- **消息去重机制**
  - 通过微信 MsgID 唯一索引去重
  - 防止微信服务器重试导致的重复消息落库
  - 重复消息记录日志但不影响业务流程

- **MessageService 对话服务层**
  - `BuildContextMessages(ctx, userID, currentContent)` - 构建上下文消息列表
  - `SaveUserMessage(ctx, userID, content, msgID)` - 保存用户消息（带去重）
  - `SaveAssistantMessage(ctx, userID, content)` - 保存 AI 回复

- **MessageRepository 对话仓储层**
  - `Create(msg)` - 创建消息
  - `GetRecentByUserID(userID, limit)` - 查询用户最近 N 条消息
  - `ExistsByMsgID(msgID)` - 检查 MsgID 是否已存在

### 🔧 架构改进

- **滑动窗口上下文**：永久存储所有消息，调用 LLM 时只取最近 10 轮（20 条）
- **降级策略**：数据库查询失败时自动降级为单轮对话模式，对用户无感知
- **消息过滤**：只有文本消息进入上下文，图片/语音/视频等非文本消息正常回复但不入库
- **失败兜底回复入上下文**：LLM 调用失败的提示也保存，避免上下文断裂

### 📦 数据变更

- 新增 `messages` 表存储所有对话消息
- 包含字段：`id`、`user_id`、`role`、`content`、`msg_id`、`created_at`
- `msg_id` 唯一索引（去重）
- `user_id` 普通索引（按用户查询）
- `role` 普通索引（user/assistant/system/tool）

### 🔐 安全增强

- 日志中不输出完整对话内容，只输出消息 ID、长度、角色等元数据

---

## [v1.1.0] - 2026-05-10

### ✨ 新增功能

- **用户自定义 LLM 配置**
  - 支持用户通过微信命令配置自己的 API Key
  - 支持自定义 API 地址（兼容所有 OpenAI 格式的服务）
  - 配置命令：`#模型设置`、`#设置Key sk-xxx`、`#设置地址 https://xxx`、`#我的配置`、`#重置模型`
  - 配置查看时 API Key 脱敏展示（只显示前后3位）
  - 重置配置后自动恢复使用系统默认模型

- **AES-256-GCM 加密模块**
  - 用户 API Key 采用 AES-256-GCM 认证加密存储
  - 数据库中无明文密钥
  - 支持环境变量 `LLM_CONFIG_ENCRYPT_KEY` 指定加密密钥
  - 每次加密使用随机 nonce，相同明文加密结果不同

- **LLM 配置 Repository 层**
  - `Create(config *LLMConfig) error` - 创建配置
  - `GetByUserID(userID int64) (*LLMConfig, error)` - 查询用户配置
  - `Update(config *LLMConfig) error` - 更新配置
  - `Delete(userID int64) error` - 删除配置
  - user_id 唯一索引，每个用户只能有一份配置

- **LLM 配置 Service 层**
  - `SetAPIKey(userID int64, apiKey string) error` - 设置 API Key（自动加密）
  - `SetBaseURL(userID int64, baseURL string) error` - 设置 API 地址
  - `GetConfigForUser(userID int64)` - 获取解密后的配置（LLM 调用时使用）
  - `GetConfigView(userID int64)` - 获取配置视图（API Key 脱敏，用于前端展示）
  - `ClearConfig(userID int64) error` - 清除用户自定义配置

### 🔧 架构改进

- **配置注入机制**：LLM 调用时优先使用用户自定义配置，配额独立
- **错误友好提示**：配置调用失败时返回用户可理解的错误信息
- **向后兼容**：无配置用户继续使用系统默认模型，不影响现有功能

### 📦 数据变更

- 新增 `user_llm_configs` 表存储用户自定义 LLM 配置
- 包含字段：`id`、`user_id`、`api_key`（加密）、`base_url`、`model`、`status`、`created_at`、`updated_at`
- `user_id` 唯一索引

### 🔐 安全增强

- AES-256-GCM 认证加密存储敏感数据
- 加密密钥通过环境变量配置，不提交代码
- 数据展示自动脱敏，防止 API Key 泄露

---

## [v1.0.0] - 2026-05-09

### ✨ 新增功能

- **微信公众号完整接入**
  - `GET /wechat/callback` - 微信服务器接入验证
  - `POST /wechat/callback` - 处理微信回调消息
  - 支持所有消息类型：文本、图片、语音、视频、短视频、位置、链接
  - 支持事件处理：关注、取消关注、菜单点击等

- **用户体系一期**
  - 关注公众号时自动创建用户账号
  - 微信账号与用户关联（OpenID / UnionID）
  - 支持多微信账号关联同一用户

- **LLM 对话集成**
  - 统一的 LLM 客户端抽象层
  - 支持 OpenAI 兼容 API
  - 所有消息类型统一调用大模型回复

- **管理 API**
  - `GET /api/v1/health` - 健康检查
  - `GET /api/v1/metrics` - 系统指标
  - `GET /api/v1/config` - 查看配置
  - `PUT /api/v1/config` - 更新配置

- **基础设施**
  - 数据库抽象层（SQLite / PostgreSQL 支持）
  - Zap 高性能结构化日志，支持文件轮转
  - Viper 配置管理，支持环境变量覆盖
  - Gin Web 框架，中间件完整

### 🗄️ 数据库表

- `users` - 用户表
- `wechat_accounts` - 微信账号关联表

---

## 版本说明

- **MAJOR 版本**：不兼容的 API 变更
- **MINOR 版本**：向后兼容的功能性新增
- **PATCH 版本**：向后兼容的问题修正

本项目采用 [语义化版本号 (Semantic Versioning)](https://semver.org/lang/zh-CN/)。
