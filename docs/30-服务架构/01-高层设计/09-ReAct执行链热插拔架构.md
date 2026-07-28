# ReAct 执行链热插拔架构 v1.0

| 项 | 内容 |
|----|------|
| 版本 | v1.0(执行链架构第一版) |
| 状态 | ✅ 已实现 |
| 关联 | 08-后台Agent任务框架技术方案(子 Agent 委托) / agent-service 模块设计 |
| 适用版本 | v1.8+ |

---

## 1. 背景与问题

### 1.1 现状(重构前)

`ReActAgent.RunStream` 是一个 ~230 行的大循环,把**纯 ReAct 推理**和多种**横切机制**硬编码在一起:

| 机制 | 原实现位置 | 问题 |
|------|-----------|------|
| 工具熔断(B) | `toolFailStreak` + `filterToolsByCircuitBreaker` + 执行跳过,散在循环 3 处 | 加/换策略要改热路径 |
| MaxSteps 强制汇总(C) | `runForcedSummary`,循环末尾硬编码 | 同上 |
| 思考模式(Thought/Final) | 循环内 if 判断发事件 | ReAct 语义,留循环 |
| 步骤记录(LLMCall/ToolResult 事件) | 循环内各处 emit | 事件产出,留循环 |

### 1.2 痛点

1. **每加机制都要改热路径**:熔断、汇总、思考模式都是这几轮加的,每次钻进 RunStream 改,循环越来越长越脆
2. **主/子 Agent 无法差异化**:同一套硬编码机制,子 Agent 必要的熔断/汇总对主 Agent 强制套上,没法差异
3. **机制难单测**:熔断/汇总的测试都得 mock 整个流式 LLM + 构造多轮 chunks,因为脱不出循环

### 1.3 目标

把 ReAct 推理循环和横切机制**边界划清**,机制抽象为**可热插拔的 hook 链**:
- ReActAgent 循环只做纯推理,瘦到 ~90 行
- 机制作为 hook 实现,构造时按需装配
- 主/子 Agent 差异化:子 Agent 装熔断+汇总,主 Agent 按需
- 新机制 = 新 hook,不动循环

---

## 2. 架构总览

```
ReActAgent.RunStream (纯推理循环,~90行)
  ├── 调 LLM -> 解析 chunks -> 组装 tool_calls -> 执行工具 -> 判断结束   ← 本职,留在循环
  └── 在固定切点调 hooks 链                                              ← 机制,可插拔

hook 链 (RoundHook 接口, 4 切点):
  ├── BeforeRound      链式过滤  -> CircuitBreakerHook(移除已禁工具)
  ├── OnLLMResult      预留      -> (当前无内置实现,审计/拦截扩展点)
  ├── OnToolExecute    短路拦截  -> CircuitBreakerHook(拦截已禁工具)
  └── OnMaxExhausted   取首个非空 -> ForceSummaryHook(无工具 LLM 汇总)

Runtime(共享状态): Ctx / Messages / Tools / FailStreak / FinalAnswer / Emit / Step
```

### 2.1 分层职责

| 层 | 职责 | 不可变? |
|----|------|---------|
| ReActAgent 循环 | 纯推理:调 LLM、解析 chunks、组装 tool_calls、执行工具、判断结束 | ✅ 本职 |
| 思考模式(Thought/Final 事件) | 回复轮 vs 思考轮的事件发射 | ✅ ReAct 语义,留循环 |
| 步骤记录(LLMCall/ToolResult 事件) | 供落 agent_steps 复盘 | ✅ 事件产出,留循环 |
| 超时 | `ctx.WithTimeout` | ✅ 留循环 |
| Runtime | 运行时共享状态,贯穿 RunStream | - |
| RoundHook 链 | 横切机制(熔断/汇总等),可插拔 | 🔌 可热插拔 |

**关键边界**:循环只通过 Runtime 与 hook 交互,不暴露局部变量。思考模式/步骤事件/超时是 ReAct 本职,不抽成 hook。

---

## 3. 核心抽象

### 3.1 Runtime(运行时共享状态)

```go
type Runtime struct {
    Ctx         context.Context            // 请求 ctx(供 hook 调 LLM,如强制汇总)
    Messages    []map[string]interface{}   // 对话历史(可读写:工具结果会 append)
    Tools       []map[string]interface{}   // 全量工具(原始,ToOpenAITools 产物,不变)
    FinalAnswer string                     // 累积最终回答(回复轮文本累加)
    FailStreak  map[string]int             // 工具连续失败计数(熔断状态)
    Emit        func(AgentEvent)           // 事件出口(emit 到 out channel)
    Step        int                        // 当前轮次(1-based)
}
```

贯穿整个 RunStream,所有 hook 通过 Runtime 读写状态。hook 不直接碰循环局部变量(边界清晰)。

### 3.2 RoundHook 接口(4 切点)

```go
type RoundHook interface {
    // BeforeRound:每轮开始,过滤本轮可用 tools(如熔断移除已禁工具)
    BeforeRound(rt *Runtime) []map[string]interface{}

    // OnLLMResult:LLM 流结束、拿到本轮文本+tool_calls 后。
    // 返回 proceed=false 表示不再执行本轮工具(预留:外部强制终止)
    OnLLMResult(rt *Runtime, content, reasoning string, toolCalls []ToolCall) (proceed bool)

    // OnToolExecute:执行单个工具前。executed=true 表示已处理(如熔断拦截),不真正 Execute
    OnToolExecute(rt *Runtime, call ToolCall) (result, status string, executed bool)

    // OnMaxExhausted:达 MaxSteps 时调用。返回汇总文本(强制汇总);空串回落兜底文案
    OnMaxExhausted(rt *Runtime) string
}
```

### 3.3 hookChain(组合模式)

`hookChain` 把多个 hook 串成链,自身实现 `RoundHook`。空链退化为 `noopRoundHook`(纯推理,无机制)。

```go
func newHookChain(hooks []RoundHook) RoundHook  // nil/空 -> noop
```

---

## 4. 切点调用规则与结果传递(当前实现)

> ⚠️ **本节如实记录当前实现**,含尚未一致/待澄清的点。后续迭代会调整。

| 切点 | 聚合规则 | 结果是否传递给下游 hook | 内置实现 |
|------|---------|----------------------|---------|
| `BeforeRound` | **链式**:上一个输出 = 下一个输入 | ✅ 传递(下游看到的是上游过滤后的 tools) | CircuitBreakerHook |
| `OnLLMResult` | 注释说广播,实现是**短路**(首个 false 即止) | ❌ 不传递(短路即停,后续 hook 不调) | 无(预留) |
| `OnToolExecute` | **短路**:首个 executed=true 即止 | ❌ 不传递(后续 hook 不调) | CircuitBreakerHook |
| `OnMaxExhausted` | 注释说广播,实现是**取首个非空**(短路) | ❌ 不传递 | ForceSummaryHook |

### 4.1 各切点详述

**BeforeRound(链式过滤)**
```
hook1.BeforeRound([a,b,c]) -> [a,b]   // hook1 移除 c
hook2.BeforeRound([a,b])   -> [a]     // hook2 在 hook1 结果上再移除 b
最终本轮 tools = [a]
```
下游 hook 看到上游过滤后的结果。适合多级过滤(熔断 + 未来工具权限隔离)。

**OnToolExecute(短路拦截)**
```
hook1.OnToolExecute(call) -> (result, status, executed=true)  // hook1 拦截
// hook2 不被调用
最终:用 result,不真正执行工具
```
第一个拦截者负责。若需"hook1 改写参数、hook2 执行"的流水线,当前不支持。

**OnLLMResult(预留,当前空转)**
所有内置 hook 都返回 `proceed=true`。链式实现是短路(首个 false 停),但当前无 hook 用它。
预期用途:审计(记录每轮 LLM 输出)、内容安全拦截(违禁内容提前终止)。

**OnMaxExhausted(取首个非空)**
```
hook1.OnMaxExhausted() -> ""          // 空,跳过
hook2.OnMaxExhausted() -> "汇总报告"   // 非空,取它
// hook3 不调
```
适合汇总场景(一人产出即可)。

### 4.2 待澄清的设计张力(已知问题)

1. **`OnLLMResult` 注释 vs 实现矛盾**:注释写"广播",实现是"首个 false 即停"(短路)。若定位为"广播 + 任一可否决",应让所有 hook 都被通知,再汇总 proceed。当前未改。
2. **`OnMaxExhausted` 同样**:注释"广播",实现"取首个非空"(短路)。
3. **hook 间结果传递不统一**:BeforeRound 流水线,其他切点短路。是否需要"工具执行后"切点(`AfterToolExecute`)也未定。

这些是 v1.0 的已知妥协,留待基于真实新机制需求迭代。

---

## 5. 内置 Hook

### 5.1 CircuitBreakerHook(工具熔断)

**目的**:同一工具连续失败达阈值(3 次)后熔断,移除/拦截后续调用,抑制子 Agent 对失败工具(如 web_reader 对 401 站点)的无限重试。

**两处切点**:
- `BeforeRound`:从本轮 tools 移除已达阈值的工具(模型看不到,无法发起调用 -- 硬约束)
- `OnToolExecute`:达阈值的工具调用直接拦截,返回禁用提示(双保险,防模型凭旧上下文再调)

**状态**:`rt.FailStreak`(在 Runtime 上共享)。循环执行工具后更新:成功清零 / 失败++。

**构造**:`NewCircuitBreakerHook(threshold int)`,默认阈值 `ToolFailureThreshold = 3`。

### 5.2 ForceSummaryHook(MaxSteps 强制汇总)

**目的**:达 MaxSteps 时不吐"已达到最大步数限制"废话,而是强制做一次无工具 LLM 调用(`tools=[]`),让模型基于已收集信息产出报告。保证最坏情况也有报告。

**切点**:`OnMaxExhausted` -- 追加 user 提示("已达到步数上限,请立即基于已收集信息产出报告,不要再调工具"),传空 tools 调 LLM,emit token(实时)+ llm_call,返回汇总文本。失败返回空(交循环回落兜底文案)。

**构造**:`NewForceSummaryHook(streamClient StreamingLLMClient)` -- 持有 LLM 客户端用于汇总调用。

---

## 6. 装配(主/子 Agent 差异化)

### 6.1 子 Agent(`sub_agent_runner.go`)

```go
svc := NewAgentService(AgentServiceConfig{
    ...
    Hooks: []RoundHook{
        NewCircuitBreakerHook(ToolFailureThreshold),  // 熔断
        NewForceSummaryHook(streamClient),            // 强制汇总
    },
})
```

子 Agent 后台跑,熔断+汇总是必要硬约束(抑制循环 + 兜底出报告)。

### 6.2 主 Agent(`routes.go`)

```go
agentSvc := NewAgentService(AgentServiceConfig{
    ...
    Hooks: []RoundHook{
        agentpkg.NewCircuitBreakerHook(agentpkg.ToolFailureThreshold),
        agentpkg.NewForceSummaryHook(agentLLMClient),
    },
})
```

主 Agent 同样装配(MaxSteps 兜底不吐废话)。

### 6.3 配置链路

`ReActAgentConfig.Hooks` -> `ReActAgent.hooks` -> `newHookChain(a.hooks)` -> RunStream 循环调用。
`AgentServiceConfig.Hooks` 透传给 `ReActAgentConfig.Hooks`(经 `runStreamWithClient`)。

---

## 7. 文件结构

```
internal/service/agent/
├── agent.go                  ReActAgent + RunStream(纯推理循环) + RunStream 切点调用
├── agent_runtime.go          Runtime + RoundHook 接口 + hookChain + noopRoundHook
├── circuit_breaker_hook.go   CircuitBreakerHook(熔断)
├── force_summary_hook.go     ForceSummaryHook(强制汇总)
├── tool_breaker_filter_test.go  filterToolsByCircuitBreaker helper + 单测(熔断过滤)
├── agent_runtime_test.go     hook 链组合规则单测
├── circuit_breaker_hook_test.go  熔断 hook 单测
├── force_summary_hook_test.go    汇总 hook 单测
└── streaming_test.go         RunStream 回归基线(熔断3/汇总2/思考/工具错误全绿)
```

---

## 8. 测试策略

1. **回归基线**:`streaming_test.go` 全套(熔断 3 个、汇总 2 个、思考模式、工具错误、多工具)必须全绿 -- 证明机制迁移后行为零变化
2. **hook 单测**:CircuitBreakerHook / ForceSummaryHook 独立测试,不依赖整个流式循环(直接构造 Runtime 调 hook)
3. **hook 链单测**:hookChain 组合规则(链式/短路/广播)单测
4. **装配测试**:子 Agent runner 装配 hook 后,端到端熔断/汇总行为不变(`sub_agent_steps_test.go` 既有用例覆盖)

---

## 9. 演进方向(未来,非当前实现)

- **`OnLLMResult` 语义澄清**:基于真实审计/拦截需求,定死"广播+否决"还是"流水线"
- **新切点**:`AfterToolExecute`(工具执行后,更新计数当前在循环写死)、`OnDone`(流结束)
- **新 hook**:限流退避(`LimiterHook`,429 时任务级延后)、工具权限隔离(`PermissionHook`,按 Agent 类型限工具集)、审计(`AuditHook`,记录每轮)
- **新机制 = 新 hook**,不动 RunStream 循环 -- 这正是热插拔架构的价值

---

## 10. 设计权衡记录

- **为何 hook 接口有 4 个方法而非拆成多个小接口**:切点固定且数量少,一个接口聚合更直观;空实现用 noopRoundHook 占位。若未来切点暴涨再拆。
- **为何思考模式/步骤事件留循环**:它们是 ReAct 语义本身(回复轮 vs 思考轮、运行链路记录),非可插拔横切机制。抽成 hook 会过度抽象。
- **为何熔断状态在 Runtime 而非 hook 内**:循环执行工具后要更新计数,状态共享比 hook 持有更简单(避免 hook 间传参)。CircuitBreakerHook 的阈值判断读 Runtime.FailStreak。
- **为何主 Agent 也装熔断+汇总**:主 Agent 同样会跑满步数/工具失败,兜底出报告而非废话。一致性优于差异化。

---

**文档版本**:v1.0
**最后更新**:2026-07-26
**实现 commit**:`31472fe refactor(agent): ReAct 执行链热插拔架构`
