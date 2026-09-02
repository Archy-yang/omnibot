# 后台 Agent 任务框架(多 Agent 委托)技术方案 v1.0

| 项 | 内容 |
|----|------|
| 版本 | v1.0(框架第一版) |
| 状态 | 🟡 设计中(待开发) |
| 决策 | 完整异步任务 + C 延迟汇报 + 框架配 1 示例子 Agent |
| 关联 | 路线图 v3.0「统一异步任务抽象」/ v1.5.4 消息表预留「异步任务独立表」 |

---

## 1. 背景与目标

### 1.1 现状

- Web 用 `AgentService.RunStream`(流式 SSE),飞书用 `AgentService.Run`(同步聚合),
  微信走 `callLLM` 直连(没用 Agent)。三入口都是**同步阻塞**:用户发消息->等 Agent 跑完->回。
- Agent 多步推理 + 工具调用可能耗时数十秒,微信 5 秒超时是死结,Web/飞书也让用户干等。
- 消息表 v1.5.4 注释已预留:「未来 artifact / 异步任务等一等实体改用独立表 + 引用 id」。

### 1.2 目标(用户模型)

> 主 Agent(总管家)对话中判断"这事该派活" -> 生成委托指令(目标+停止条件+汇报格式)
> -> 子 Agent 后台独立执行 -> 主 Agent 立即返回继续聊 -> 子 Agent 完成后按固定格式回执
> -> 主 Agent 识别回执,整合进对话转述给用户。

第一版定:
- **完整异步**:派活即返回,子 Agent 后台跑,不阻塞主对话
- **延迟汇报(C 模式)**:子 Agent 完成只更新任务表;用户下次发消息时,主 Agent 先汇报未汇报的已完成任务,再回应当前消息。不依赖推送通道。
- **固定子 Agent**:框架配 1 个示例子 Agent(研究员),验证框架;后续按需加。
- **A2A 协议语义,进程内实现**:用 A2A 概念(Agent Card / Task / Artifact / 状态机),但子 Agent 是进程内执行,不走 HTTP。为未来分布式/接外部 Agent 留路。

### 1.3 非目标(第一版不做)

- ❌ 主动推送(B 模式,需 SSE/客服消息推送通道)--第一版用前端轮询 + report 接口替代
- ❌ 多个子 Agent 并行编排(第一版一次派一个)
- ❌ 子 Agent 运行时动态配置(第一版固定注册)
- ❌ 子 Agent 跨进程/真 A2A HTTP(进程内实现)
- ❌ 用户自定义子 Agent(第一版系统预设)
- ❌ 微信入口接入(微信走 callLLM 非 Agent,5s 超时;第一版只做 Web + 飞书)

---

## 2. 核心架构

```
用户消息(Web/飞书)
   ↓
主 Agent RunStream(system prompt 含 delegate 工具)
   ├─ LLM 决定调 delegate 工具 -> SubAgentService.StartTask(...) -> 立即返 task_id
   │     ↓(后台 goroutine)
   │   子 Agent(独立 AgentService.Run,独立 prompt+工具) -> 写 Artifact 到任务表
   │
   ├─ LLM 决定不派活 -> 正常对话回复(流式)
   ↓
主 Agent 回复用户(含"已安排X处理"自然语言确认)

=== 汇报阶段(前端轮询触发,方案B + 单独接口) ===

前端定时轮询 GET /api/v1/agent/tasks?status=completed_unreported
   ↓(发现有完成的任务)
前端调 POST /api/v1/agent/tasks/:id/report
   ↓
后端 report handler:
   取 task.Artifact -> 构造 conversation=[{system: 回执格式},{user:"请汇报此任务结果"}]
   -> 主 Agent RunStream 流式汇报 -> SSE 推前端
   ↓
前端把汇报作为正常流式助手消息渲染在对话框
   ↓
汇报完调 MarkReported(taskID),不再重复汇报

=== 兜底:用户发消息时前置查(防漏) ===
用户发消息 -> 主 Agent RunStream 前置查 completed_unreported
   ├─ 有 -> 把回执注入上下文,主 Agent 先汇报再回应当前消息
   └─ 无 -> 正常处理
(轮询是主路径,前置查兜底防漏;两者都靠 Reported 字段去重)
```

---

## 3. A2A 协议语义(进程内子集)

### 3.1 子 Agent:通用执行器(去角色,已移除 Agent Card)

> **v1.10+**:已移除 `SubAgentCard`/`SubAgentRegistry`(角色卡注册层,见 §5.7)。子 Agent 是**通用执行器**,
> 身份 = 任务合同(TaskSpec),不再有"researcher"等框架枚举角色。persona 是任务级自由文本 `persona_hint`
> (主 Agent 按任务给)。工具可见性由「工具能力标签 ∩ config 白名单」算出(§5.6)。

子 Agent 的 system prompt 由 `agentprompt` 注册式组装,固定结构:

```
agent_base(-100)  共享基础人格(DefaultSystemPrompt,与主 Agent 同款)
sub_role(0)       通用执行器 persona(SubAgentExecutorPersona,含收敛规则)
[sub_persona_hint](50)   任务级角色 hint(主 Agent 传,可空) → 【本次任务角色】
sub_contract(100) 任务合同(deliverables/criteria/background/constraints)
```

`TaskSpec` 新增两字段承载去角色语义:
- `Type string`:任务溯源标签(可空),落 `AgentTask.SubAgentType`,仅供展示/溯源,不 gate 任何机制。
- `PersonaHint string`:任务级角色扮演提示(可空),注入子 Agent prompt。

### 3.2 Task(任务生命周期)

`internal/domain/agent/agent_task.go`:
```go
type AgentTask struct {
    ID          int64
    UserID      int64            // 归属用户(任务按用户隔离)
    SubAgentType string          // 溯源标签(可空,来自 taskSpec.Type;去角色后非角色,不 gate 机制)
    Goal        string           // 主 Agent 生成的委托目标(已填入 prompt)
    Status      string           // pending / running / completed / failed
    Artifact    *string          // 子 Agent 最终产出(JSON 或文本),completed 时填
    ErrorMsg    *string          // failed 时填
    Reported    bool             // 是否已汇报给主 Agent(C 模式核心字段)
    CreatedAt   time.Time
    StartedAt   *time.Time
    CompletedAt *time.Time
}
// TableName: agent_tasks
```

状态机:`pending -> running -> completed | failed`。`reported` 独立 bool,completed 后等主 Agent 汇报才置 true。

### 3.3 委托调用协议

主 Agent 通过 **delegate 工具**(Function Calling)派活,工具参数:
```json
{
  "sub_agent_type": "researcher",
  "goal": "研究 2026 年 Go 1.24 的新特性,总结要点"
}
```
delegate 工具 Execute **立即返回**(异步):
```json
{"task_id": 123, "status": "pending", "message": "已安排研究员处理,稍后汇报"}
```
主 Agent LLM 收到这个工具结果,生成自然语言确认回用户("好的,我让研究员去查一下 Go 1.24 新特性,稍后告诉你")。

### 3.4 回执格式(固定)

子 Agent 完成,Artifact 存任务表。主 Agent 汇报时,回执注入上下文的固定格式:
```
[子任务完成回执]
任务ID: 123
子Agent: 研究员
目标: 研究 2026 年 Go 1.24 的新特性
结果:
<Artifact 内容>
```
主 Agent system prompt 指示:看到此格式先向用户汇报该任务结果,再处理用户当前消息。

> **修订(2026-09-03,汇报锚定)**:回执除目标外还带**完整任务合同**(背景/交付物/完成标准,
> 来自 task_spec),汇报指令要求主 Agent **对照完成标准自查达标性、如实汇报不粉饰**;
> report 接口与飞书主动推送的汇报上下文**前置最近对话历史**(BuildContextMessages),
> 锚定"用户当初为什么派这个活"并延续对话口吻。web/飞书前置汇报本就走正常对话轮(自带历史)。

---

## 4. 分层文件计划

### 4.1 Domain 层

- `internal/domain/agent/sub_agent_card.go` -- SubAgentCard
- `internal/domain/agent/agent_task.go` -- AgentTask + 状态常量 + TableName

### 4.2 Repository 层

- `internal/repository/agent/agent_task_repo.go`:
  - `Create(task) error`
  - `GetByID(id) (*AgentTask, error)`
  - `UpdateStatus(id, status, artifact/errorMsg) error`
  - `MarkReported(id) error`
  - `ListCompletedUnreported(userID) ([]*AgentTask, error)` -- C 模式核心查询
  - `ListByUser(userID, limit) ([]*AgentTask, error)` -- 用户查看自己的任务

### 4.3 Service 层

- `internal/service/agent/registry.go` -- SubAgentRegistry:注册/查询 SubAgentCard
- `internal/service/agent/sub_agent_service.go` -- SubAgentService:
  - `StartTask(ctx, userID, subAgentType, goal) (taskID, error)` -- 建任务 + 起 goroutine 执行
  - `executeTask(task)` -- 私有:用子 Agent 的 prompt+工具跑 AgentService.Run,写 Artifact
  - `GetCompletedUnreported(userID)` -- 供主 Agent 前置查询
  - `MarkReported(taskID)` -- 汇报后标记

### 4.4 主 Agent 集成

- `internal/service/agent/builtin_tools.go` 新增 `CreateDelegateTool(subAgentSvc)`:
  - 工具描述列出所有已注册子 Agent 的能力(动态生成,主 Agent LLM 据此派活)
  - Execute 调 `subAgentSvc.StartTask`,立即返 task_id
- 主 Agent system prompt 增补两段:
  1. "你有 delegate 工具可派子 Agent 处理耗时任务。派活后告诉用户已安排,不要让用户等。"
  2. "若上下文有[子任务完成回执],先汇报该结果再回应当前消息。"

### 4.5 汇报触发(轮询主路径 + 发消息兜底)

**主路径:前端轮询 + report 接口(方案B + 单独接口)**

前端定时(约 8s)轮询 `GET /api/v1/agent/tasks?status=completed_unreported`,
发现有完成的任务 -> 调 `POST /api/v1/agent/tasks/:id/report`:
- 后端 report handler:取 task.Artifact -> 构造 conversation =
  `[{role:system, content: 回执格式}, {role:user, content: "请向用户汇报此任务的结果"}]`
  -> 主 Agent RunStream 流式汇报 -> SSE 推前端
- 前端把汇报作为正常流式助手消息渲染在对话框(复用现有 ChatMessage 流式逻辑)
- 汇报完调 `MarkReported(taskID)`,不重复汇报

report 接口是独立 SSE 流,不混入用户对话历史(不落 messages 表),
仅作为「子任务汇报」这条助手消息呈现。

**兜底:用户发消息时前置查(防漏)**

web `HandleSendMessageAgentStream` / 飞书 `HandleInbound`:
在调主 Agent Run 前,查 `GetCompletedUnreported(userID)`:
- 有 -> 把回执拼进 conversation 上下文(作为 system 消息),主 Agent 先汇报再回应当前消息
- 无 -> 正常流程

轮询是主路径(用户不用发消息也能收到汇报),前置查兜底防漏(轮询间隙用户发消息时也能汇报)。
两者都靠 `Reported` 字段去重,不会重复汇报。

### 4.6 装配(routes.go)

- 建 `agentTaskRepo` + `subAgentRegistry` + `subAgentSvc`
- 注册示例子 Agent "researcher"(prompt/角色;工具由能力白名单裁剪,见 §5.6)
- delegate 工具加入主 Agent toolRegistry
- subAgentSvc 注入主 Agent handler(前置汇报兜底用)
- 新增路由:`GET /api/v1/agent/tasks`(轮询)+ `POST /api/v1/agent/tasks/:id/report`(触发汇报)
- 仅 Web + 飞书入口接入(微信第一版不接入)

### 4.7 用户可见性(第一版必做)

- `GET /api/v1/agent/tasks?status=completed_unreported` -- 前端轮询用,返回未汇报的已完成任务
- `GET /api/v1/agent/tasks` -- (可选)列出当前用户全部任务及状态,供未来「任务面板」用
- `POST /api/v1/agent/tasks/:id/report` -- 触发主 Agent 流式汇报该任务
- 第一版前端做轮询 + 自动触发汇报;「任务面板」(用户主动查看全部任务)留后续

---

## 5. 关键设计决策

### 5.1 子 Agent 用谁的 LLM 配置?

**系统默认**,不用用户自定义配置。
理由:用户自定义 LLM 配置是给主对话的(用户在用);子 Agent 后台跑,用系统默认 provider 更稳,
且避免用户配的廉价模型撑不起子 Agent 多步推理。后续可扩展"子 Agent 继承用户配置"开关。

### 5.2 子 Agent 上下文隔离

子 Agent **独立上下文**,不继承主对话历史。
- 子 Agent 只拿到主 Agent 生成的 goal(委托目标),不看到主对话其他消息
- 子 Agent 的运行链路(agent_steps)按 task_id 落库,不混入主对话 messages
- 子 Agent 完成只把 Artifact(最终产出)回传,中间过程不进主对话

### 5.3 汇报触发的边界(轮询 + 兜底)

- 主路径:前端轮询发现 completed_unreported -> 调 report 接口触发主 Agent 流式汇报。
  用户不用发消息也能收到汇报(比原 C 模式"等用户下次发消息"更主动)。
- 兜底:用户发消息时前置查未汇报任务(防轮询间隙漏报)。
- 极端情况:用户关闭页面(前端不轮询)且不发消息 -> 任务结果压在任务表,等下次活跃时汇报。
  第一版接受(个人项目;未来 B 模式主动推送可彻底解决)。
- 任务表 `Reported` 字段保证轮询触发和兜底触发不会重复汇报。
- 未来要 B 模式(服务端主动推送)时,在 SubAgentService.executeTask 完成回调里加推送即可,
  任务表/回执格式/report 接口都不用改,前端轮询可逐步下线。

### 5.4 并发与持久化

- 子 Agent 在 goroutine 后台跑,进程重启会丢失运行中的任务(第一版接受,任务表留 pending/running 记录)
- 同一用户可派多个任务(无第一版限制),每个独立 task_id
- 任务表按 user_id 隔离,不串号

### 5.5 主 Agent 派活的可靠性

主 Agent 用 Function Calling 调 delegate 工具--和现有 get_current_time/calculator 同机制,
已验证可用。LLM 据 delegate 工具描述(列出子 Agent 能力)决定是否派活。
风险:LLM 可能该派不派/不该派乱派。第一版靠 system prompt + 工具描述调优,不做硬规则。
若后续发现不可控,可加规则层(如"消息含'研究/调研'关键词才允许派 researcher")。

### 5.6 子 Agent 工具可见性(capability 标签 ∩ config 白名单)

**问题**:v1.10 前子 Agent 可见工具由 `SubAgentCard.Tools` 硬编码,把"研究员属于哪些工具"这层角色分类学写死进抽象框架(与已删的派活规划器同味)。

**做法**(仿 DSH ToolProviderResult,见 `internal/service/agent/tool_provider.go`):
- 每个 `Tool` 打 `Capabilities` 标签(取值域 `basic/memory/research/web/ingest/interactive`)。
- config `agent.sub_agent.allowed_capabilities` 声明子 Agent 可见能力白名单(默认 `[research, interactive]`)。
- 运行期 `ResolveSubAgentTools` 算出 `visible = 能力∩白名单 ∪ request_input 基线`(恒可见,修复 #19 request_input 从未注入子 Agent 的间隙)。
- **knownNames/schemas 分离**:白名单出现框架能力词汇表外的字符串 → 启动报错(配错显性);某个工具能力未命中白名单 → 仅隐藏、不报错。

**收益**:分类轴从"角色"下沉到"工具能力",可组合非枚举;子 Agent 是通用执行器,换能力只要改 config,不动 ReAct/prompt/runner。逻辑层保持抽象,运行层受配置文件约束。

### 5.7 子 Agent 去角色(通用执行器 + 任务级 persona hint)

**问题**:v1.10 前子 Agent 身份是 `SubAgentCard.Type`="researcher" 等固定角色,把"研究员属于哪类"写死进抽象框架(与已删派活规划器同类)。工具能力化(§5.6)已解放工具,但角色本身还在(卡身份、delegate 必填 sub_agent_type、主 prompt 硬写研究员)。

**做法**(已落地):
- **删 `SubAgentCard`/`SubAgentRegistry`**:框架不再有角色卡注册层。子 Agent = 通用执行器,身份 = TaskSpec。
- **delegate 去必填 `sub_agent_type`**:改传 `goal + deliverables + completion_criteria + background`,可选 `persona_hint`(任务级角色)+ `task_type`(溯源标签)。
- **sub prompt 固定结构**:共享基础人格 + `SubAgentExecutorPersona` + 可选【本次任务角色】+ 任务合同。scope 从 `sub:<type>` 收敛为单一 `ScopeSub`。
- **`TaskSpec.Type`** = 溯源标签(落 `AgentTask.SubAgentType`,仅展示)。

**取舍**:persona 从"框架枚举的卡片"改为"任务级自由文本 hint"(主 Agent 每次委托按任务给,可空)。好处是零领域知识进框架、完全抽象;代价是研究类任务产出质量依赖主 Agent 把 deliverable/criteria/persona 写清楚——主 prompt 已加模板引导,默认 `persona_hint` 空时退化为基础执行器 + 任务合同。运行时执行预算归框架、**不给 LLM 填**(防乱设):MaxSteps = `TaskSpec.Constraints.MaxSteps` 覆盖 or 框架默认;Timeout = 子 Agent 专属 `agent.sub_agent.timeout`(默认 **180s**,`DefaultSubAgentTimeout`;注意勿误用主 Agent `DefaultTimeout` 120s,会误杀耗时研究任务)。

---

## 6. 子 Agent:通用后台执行器(去角色后无固定示例子 Agent)

去角色后不再预注册 researcher 卡。任何耗时任务都委派给通用执行器,任务本身决定一切:

- delegate 委派一个任务 = `goal(必) + deliverables + completion_criteria + background` + 可选 `persona_hint` + `task_type`。
- 子 Agent system prompt = 共享基础人格 + 通用执行器 persona(含收敛规则:工具失败换路线、达成完成标准即产出)+ 可选【本次任务角色】+ 任务合同。
- 可见工具由能力白名单决定(§5.6)。默认白名单 `[research, interactive]` 下可用 = `rss_reader/web_read/search_memories/search_history` + 强制基线 `request_input`。

**委托示例**(带 persona_hint):
```json
delegate({
  "goal": "调研三个自部署 LLM 网关的选型",
  "deliverables": [{"name":"candidate_list","description":"候选方案清单"},
                   {"name":"recommendation","description":"推荐顺序和理由"}],
  "completion_criteria": ["至少比较三个方案", "给出明确推荐"],
  "persona_hint": "你是严谨的技术研究员,先多路检索,再出带来源的结构化报告"
})
```

---

## 7. TDD 开发顺序

1. **Domain + Repo**:AgentTask 实体 + TaskSpec(Type/PersonaHint) + AgentTaskRepository + 单测 + AutoMigrate
2. **SubAgentService**:StartTask(建任务+起goroutine)/executeTask(跑子Agent写Artifact)/GetCompletedUnreported/MarkReported + 单测(mock Runner)
3. **delegate 工具**:CreateDelegateTool(去 sub_agent_type,带 persona_hint/task_type) + 单测
5. **主 Agent 前置汇报**:主对话流程改造(查未汇报任务+注入回执)+ 单测
6. **装配 + 示例子 Agent**:routes.go 注册 researcher + 装配 subAgentSvc + delegate 工具
7. **回归 + 手测**:go test + 起后端真实派活验证(需真实 LLM)

---

## 8. 不做(第一版)

见 1.3。主动推送 / 多子 Agent 并行 / 动态配置 / 跨进程 A2A / 用户自定义子 Agent / 任务查询接口(可选)。

---

## 9. 风险

- **LLM 派活可靠性**:主 Agent 可能不稳定地决定派活。靠 prompt 调优,第一版接受。
- **子 Agent 失败处理**:超时/达最大步数/LLM 错误 -> 任务 failed,ErrorMsg 落库,
  延迟汇报时主 Agent 也会汇报失败("你让我研究X,但研究员没能完成:超时")。
- **进程重启丢运行中任务**:第一版接受(pending/running 留记录但不自动恢复)。
  未来加启动时扫描 running 任务标 failed + 通知。
- **子 Agent 工具结果不脱敏进主对话**:Artifact 是子 Agent 最终产出(报告),非原始工具结果,
  脱敏在子 Agent 侧已做(沿用 v1.5.5 sanitizeToolResult)。Artifact 进主对话无额外风险。
