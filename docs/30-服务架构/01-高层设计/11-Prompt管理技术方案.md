# Prompt 管理技术方案 v1.0（借鉴 DSH）

| 项 | 内容 |
|----|------|
| 版本 | v1.0（Prompt 注册式管理第一版） |
| 状态 | ✅ 已实现（Track A：内容抽独立包 agentprompt；能力组装与文件化后置） |
| 关联 | 08-后台Agent任务框架技术方案 / 09-ReAct执行链热插拔架构 / agent-service 模块设计 |
| 适用版本 | v1.9+ |
| 借鉴来源 | DeepSeek Harness（DSH）`system-prompt` 子系统 |

---

## 0. 本期范围

**只做 prompt 管理本身**，DSH 概念按需取舍，其余**本期不做**：

| DSH 概念 | 本期？ | 说明 |
|---------|:---:|------|
| PromptSection 注册 + Order 排序 + 按 Scope 组装 | ✅ | 本文档核心 |
| text 静态 / provider 函数 / `{{variable}}` 插值 | ✅ | 动态内容注入 |
| `complete` 逃逸口（某 section 独占整个 prompt） | ✅ | 特殊模式 |
| 替换现在的 `hasSubAgents` 布尔 | ✅ | 用「该 section 是否注册」表达 |
| 主/子 Agent prompt 统一走同一机制 | ✅ | 消灭两套割裂拼接 |
| ToolProviderResult（按 scope 裁剪工具可见集） | ❌ 后置 | 本期不设计 |
| Cordis 插件生命周期 / 作用域副作用（`change` 事件） | ❌ 后置 | 不引入框架 |

> 设计原则：先把「同一条 prompt 由哪些片段、按什么顺序、对谁」**用数据表达**，而不是散落在几个拼接函数里。

---

## 1. 背景与问题

### 1.1 现状（重构前）

prompt 构造散落在多处，且主/子 Agent 用**两套互不相通的机制**：

| Agent | 现状 | 问题 |
|-------|------|------|
| 主 Agent | `MainAgentSystemPrompt(hasSubAgents bool)`（`agent.go`）一个大字符串拼接 `defaultSystemPrompt` + 派活规则 + 汇报规则 + 任务管理 | 一整块字符串，动一段要翻整个函数；`hasSubAgents` 布尔控制整块是否出现，无法细粒度组装 |
| 子 Agent | `SubAgentCard.PromptTemplate`（domain）+ `buildSubAgentPrompt`（`sub_agent_runner.go`）用 `strings.ReplaceAll("{goal}")` 填值 + 注入任务包详情 | 朴素字符串替换，只有 goal 一个占位；主/子提示词无法共享同一条 persona/框架身份 |

### 1.2 痛点

1. **prompt 是"死字符串"**：主 Agent 那一段是代码里硬写的拼接，增删规则改热代码，且没法按 Agent/场景差异化。
2. **主/子两套机制割裂**：主 Agent 没有 `{goal}` 这类占位体系；子 Agent 的 `PromptTemplate` 也不参与任何统一组装。
3. **`hasSubAgents` 是脆弱的布尔开关**：决定"派活规则/工具是否出现"全靠一个 bool 贯穿，语义不可见、扩展难。
4. **动态内容靠运行时硬拼**：「当前可用子 Agent 清单」、用户 LLM 配置这类动态信息直接拼进字符串，难以结构化复用。
5. **未来"派活规划器回归"无处安放**：上一次删除规划器就是硬删代码/再硬加回。若用 section 化，**要不要出现 = 注册不注册一段 section**，改动降到只动组装点。

### 1.3 目标

把 prompt 构造从"散落的拼接函数"收敛为一个**注册式组装中心**：

- prompt 拆成**命名 section**，各自注册进 `PromptRegistry`，按 **Order** 升序拼成一个完整 system prompt
- 同一 registry 按 **Scope** 组装，主 Agent / 各类子 Agent 各自拿自己的 prompt，又共享全局身份段
- **动态内容**用 provider 函数或 `{{variable}}` 插值，按本次请求上下文求值
- 主/子 Agent 走**同一套机制**，消灭割裂
- `hasSubAgents` 布尔被「该 section 是否注册」取代

---

## 2. 架构总览

```
PromptRegistry(注册中心, 进程内单例或按 AgentService 装配)
  ├── sections : 命名 + 作用域 + 排序 + 文本(静态/provider)
  ├── variables: {{name}} 插值 provider
  └── Assemble(c PromptCtx) -> 最终 system prompt(string)

组装输入 PromptCtx
  ├── Scopes  []ScopeKey   // 本次要谁喝: main / sub:researcher ...
  └── Request map[string]string  // 本次运行动态数据: goal/deliverables/用户配置...

消费方
  ├── 主 Agent : systemPrompt = registry.Assemble({Scopes:[main]})
  └── 子 Agent : systemPrompt = registry.Assemble({Scopes:[sub:researcher], Request:{goal:...}})
```

**关键边界**：`PromptRegistry` 只负责"产出最终的 system prompt 字符串"，不碰工具、不碰循环。`ReActAgent` / `AgentService` 依然消费 `SystemPrompt string`，改动只在**生产这个字符串的地方**。

---

## 3. 核心抽象

### 3.1 ScopeKey（作用域键）

标识一类 Agent / 装配场景。**空串 = 全局**（所有组装都包含）。

```go
// ScopeKey 作用域键:决定哪些 section 参与某次组装。
type ScopeKey string

const ScopeMain ScopeKey = "main" // 主 Agent

// 子 Agent 用 ScopeKey("sub:" + card.Type),如 "sub:researcher"、"sub:xxx"。
func SubScope(agentType string) ScopeKey { return ScopeKey("sub:" + agentType) }
```

### 3.2 PromptCtx（组装请求上下文）

```go
// PromptCtx 一次 Assemble 的请求:要谁(命中的作用域) + 本次运行的动态数据。
type PromptCtx struct {
    Scopes  []ScopeKey        // 命中的作用域(子 Agent 一次可能只传一个)
    Request map[string]string // 本次运行动态数据(key: "goal"/"deliverables"/"criteria"/"user_llm_config"...为已格式化文本)
}
```

### 3.3 PromptSection（一条可注册的提示词片段）

```go
// Provider 每次组装按请求上下文求值该 section 的动态文本。
type Provider func(c PromptCtx) string

type PromptSection struct {
    Name     string    // (Scope, Name) 唯一;同作用域内重复注册抛错
    Scope    ScopeKey  // "" = 全局,所有组装参与
    Order    int       // 排序,升序拼接
    Text     string    // 静态文本;Text 与 Provider 二选一
    Provider Provider  // 动态求值;两者都空则该 section 不产文本
    Complete bool      // true = 该 section 独占整个 system prompt(逃逸口)
}
```

**构造便捷方法**（可选，不强制）：
`Static(name, scope, order, text)` / `Dynamic(name, scope, order, provider)`。

### 3.4 PromptRegistry（注册中心 + 组装入口）

```go
type Variable func(c PromptCtx) (string, error)

type PromptRegistry struct {
    sections  []PromptSection
    variables map[string]Variable
}

func NewPromptRegistry() *PromptRegistry

// Register 注册 section。(Scope, Name) 重复抛错。
func (r *PromptRegistry) Register(s PromptSection) error

// RegisterVariable 注册 {{name}} 插值 provider。name 须匹配 [a-z][a-z0-9_]*。
func (r *PromptRegistry) RegisterVariable(name string, fn Variable) error

// Has 查询某 section 是否已注册(替换 hasSubAgents 这类布尔判断)。
func (r *PromptRegistry) Has(scope ScopeKey, name string) bool

// Assemble 组装最终 system prompt,返回拼接+插值后的字符串。
func (r *PromptRegistry) Assemble(c PromptCtx) (string, error)
```

### 3.5 变量插值 vs provider —— 什么时候用哪个

| 机制 | 适用 | 副作用 |
|------|------|--------|
| `Provider`（section 动态函数） | 整段是动态的、或要按 context 组装多段细节（如任务包详情 deliverable/criteria） | 一段一个函数，直白 |
| `{{name}}` 插值 + `RegisterVariable` | 长静态文本里**穿插个别替换值**（如 `目标:{{goal}}`） | 可复用、可单测；引用缺失变量 → 抛错 |

> 常规约定：**静态为主、动态为辅**。80% 的 prompt 是静态 section，只有真正跑起来才变的地方才用 provider / 变量。

---

## 4. 组装流程（Assemble 算法）

```
Assemble(c PromptCtx):
 1. 收集参与 section
      遍历注册表:保留 Scope==""(全局) 或 Scope 命中 c.Scopes 的 section
 2. Complete 逃逸口
      若参与列表中 Complete==true 的多于 1 个 -> 返回错误
      恰好 1 个完整/它独占 -> 只渲染它这一条,跳过拼接(仍走变量插值)
      0 个 -> 正常走 3~5
 3. 排序
      按 Order 升序稳定排序
 4. 拼接
      逐条取文本:Provider 非空走 Provider(c),否则取 Text;空文本跳过
      拼接为一个大字符串
 5. 插值
      正则替换 {{name}}:查 variables,缺失抛错;多段之间不跨投喂(简单逐项替换即可)
 6. 返回最终 system prompt
```

**Order 排序约定**（对齐 DSH 的"身份 → 人格 → 能力 → 动态"）：

| Order 段 | 放什么 | 例 |
|---------|--------|----|
| -100 | 框架身份 | "你是全平台智能助手"（全局共享） |
| 0 | 部署人格/角色 | 主 Agent=管家；子 Agent=研究员 |
| 100–199 | 能力/工具指引 | 派活规则、任务管理工具、收敛规则 |
| 200+ | 动态/本次运行 | 目标、任务包详情、汇报引导 |

---

## 5. 装配：主/子 Agent 统一走同一机制

### 5.1 主 Agent（scope=main）

| section | Scope | Order | 内容来源 |
|---------|:---:|:---:|----------|
| `harness_identity` | 全局 | -100 | "你是全平台智能助手"（与子 Agent 共享） |
| `persona` | main | 0 | 管家定位（从 `defaultSystemPrompt` 扩容而来） |
| `delegation_rules` | main | 100 | 派活规则（**仅装配了子 Agent 时注册**） |
| `reporting_rules` | main | 110 | 汇报引导 |
| `task_mgmt` | main | 120 | 任务管理工具(query/update/cancel)指引 |

> `hasSubAgents` 布尔**消失**：组装点不再传 bool，而是"子 Agent 支持开启时就 `Register(delegation_rules/reporting/task_mgmt)`，否则不注册"。语义变成数据：`registry.Has(main, "delegation_rules")` 为真 ⇔ 派活规则在场。

**未来"派活规划器回归"的安放处**：规划器要不要出现，就是注册不注册一段 `planner_hint` section，不动循环、不删代码。

### 5.2 子 Agent（scope=sub，去角色后通用执行器）

| section | Scope | Order | 内容来源 |
|---------|:---:|:---:|----------|
| `agent_base` | sub | -100 | 共享基础人格（`DefaultSystemPrompt`，与主 Agent 同款） |
| `sub_role` | sub | 0 | 通用执行器 persona（`SubAgentExecutorPersona`，含收敛规则；不再有角色模板） |
| `sub_persona_hint` | sub | 50 | `TaskSpec.PersonaHint` 非空才注册 → `【本次任务角色】{hint}`（任务级角色，主 Agent 按任务给） |
| `sub_contract` | sub | 100 | 任务包详情（deliverable/criteria/background/constraints，`spec.HasDetail()` gate） |

> 去角色（v1.10+）：不再有 `SubAgentCard.PromptTemplate` 角色模板，子 Agent 是通用执行器。persona 由任务级
> `persona_hint` 承载，scope 由 `sub:<type>` 收敛为单一 `sub`。`buildSubContract` 承担任务合同单一来源。

---

## 6. `complete` 逃逸口

某次要一个**极简/固定 prompt**（如纯闲聊模式、调试模式)时，注册一条 `Complete: true` 的 section。组装时它独占整个 system prompt，忽略其它 section（但仍解析其内 `{{var}}`）。多于一条 complete 抛错，防意外。

---

## 7. 迁移路线（改两处生产 prompt 的地方，其余不动）

**核心不变量**：迁移后，对同一配置产出的 system prompt **与现状逐字节一致**（金丝雀保证，防 prompt 回归）。

| 步骤 | 动作 | 验收 |
|------|------|------|
| 1 | 新增 `internal/service/agent/prompt.go`：`ScopeKey` / `PromptCtx` / `PromptSection` / `PromptRegistry` | `go build ./...` 通过 |
| 2 | 把 `MainAgentSystemPrompt` 的单条大串拆成上述 sections，写一个 `BuildMainAgentRegistry(hasSubAgents)` 组装 | 对 `hasSubAgents ∈ {true,false}`，`Assemble` 输出 == 现 `MainAgentSystemPrompt(...)` |
| 3 | 把 `researcherSystemPrompt` 的 `{goal}` 改 `{{goal}}`，另立 `goal_details` section；`buildSubAgentPrompt` 改为走 `Assemble` | 对同一 `goal + 详情`，输出 == 现 `buildSubAgentPrompt` |
| 4 | `routes.go` 用 registry 生产 `AgentServiceConfig.SystemPrompt`；`AgentService` 内部改为透传组装后的 string（ReAct 循环不动） | `service.go` 的 `runStreamWithClient` 仍消费 `SystemPrompt string`，**零变化** |
| 5 | 删 `MainAgentSystemPrompt` / `buildSubAgentPrompt` 旧实现（或在金丝雀测试通过后移除） | `grep` 无残留 |

> 收益兑现点：第 5 步后，增删规则 = 注册/不注册一段 section；新增子 Agent 类型 = 注册 `sub_role` section；调整顺序 = 改 Order。主/子人格可共享 `harness_identity`。

---

## 8. TDD 测试计划（先写测试，再写实现）

| 测试 | 断言 |
|------|------|
| `TestAssemble_StaticOrdering` | 两条静态 section 按 Order 升序拼接 |
| `TestAssemble_Scoping` | 全局 section + scope section；不传 scope 时 scope section 不参与 |
| `TestAssemble_CompleteEscapeHatch` | complete section 独占；两条 complete → 抛错 |
| `TestAssemble_VariableInterpolation` | `{{name}}` 被解析；缺失变量 → 抛错 |
| `TestRegister_Duplicate` | 同 (Scope,Name) 重复注册 → 抛错 |
| `TestProvider_DynamicSection` | provider 按 `c.Request` 求值；空 Text 空 Provider 不产文本 |
| `TestMainAgentMigration_Golden` | 拆后 `Assemble` 输出 == 现 `MainAgentSystemPrompt`（true/false 两档） |
| `TestSubAgentMigration_Golden` | 拆后 `Assemble` 输出 == 现 `buildSubAgentPrompt`（同 goal+详情） |
| `TestHas` | `Has(main,"delegation_rules")` 随注册与否返回 true/false |

---

## 9. 验收标准

1. 主/子 Agent prompt 由 `PromptRegistry.Assemble` 唯一产出，无第二处拼接。
2. 金丝雀测试证明迁移后输出与现状逐字节一致，prompt 零回归。
3. 主/子共享 `harness_identity` section，主/子各自 scope 差异化。
4. `hasSubAgents` 布尔从 prompt 装配路径移除，由 `Has(...)` 取代。
5. 新加/删一段 prompt 规则只动注册点，不改 ReAct 循环。
6. `go build ./...` + `go test ./...` 全绿（既有 auth 抖动另计，与本方案无关）。

---

## 10. 后续 & 落地进度

- **ToolProviderResult ✅ 已落地（v1.10）**：工具可见集按 `内部/service/agent/tool_provider.go` 裁剪，与 prompt scope 概念对齐——但裁剪轴取**工具自身能力标签 ∩ config 白名单**，非角色卡固定列表（DSH 的 knownNames/schemas 分离：knownNames=框架能力词汇表判定配错，visible=能力命中+request_input 基线）。详见 08 框架文档 §5.x。
- **生命周期 / 事件**：是否需要 Cordis 那套"注册即 disposer + change 事件"，取决于是否要多 Agent 动态装配；本期以纯 registry 起步。
- **持久化快照**：DSH 用 `PromptContext` 物化动态上下文快照、变更才记日志，供复盘/审计，可后续引入。

---

**文档版本**：v1.0
**创建日期**：2026-08-27