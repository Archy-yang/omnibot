# 多 Agent 通讯协议演进规划 v1.0

| 项 | 内容 |
|----|------|
| 版本 | v1.0(演进规划,非终局设计) |
| 状态 | 🟡 规划中(第一阶段待开发) |
| 关联 | 08-后台Agent任务框架技术方案 / 09-ReAct执行链热插拔架构 |
| 适用版本 | v1.9+ |

---

## 1. 背景与决策

### 1.1 起点

当前主子 Agent 通讯是**进程内函数调用 + 共享 DB**:
- 主->子:`delegate` 工具调 `SubAgentService.StartTask`,传裸 `goal` 字符串
- 子->主:artifact 落 `agent_tasks.artifact`(一坨 text),主 Agent 下次对话轮询 `ListCompletedUnreported` 读回执

够用,但有实质缺陷(见 1.2)。

### 1.2 当前缺陷(驱动本次演进)

1. **delegate 只传裸 goal**:子 Agent 无背景/交付物/完成标准,靠 LLM 自觉判断"做到什么程度算完",导致循环不收敛(task 15/16/17)
2. **artifact 是自由文本**:主 Agent 拿到一坨 text,无法按字段取用,要再解析
3. **子 Agent 不能主动要输入**:只能主 Agent 单向 `update_task` 补充,子 Agent 不能 `input_required`
4. **结果回流靠轮询**:`ListCompletedUnreported` 每次对话查,无事件驱动,延迟高
5. **大内容塞上下文**:artifact 全文进主 Agent 上下文,token 浪费

### 1.3 核心决策

**不照搬 A2A 协议全盘内部化,而是分阶段补能力,协议化等真实需求。**

判断依据:内部协作(同进程/共享 DB/互信)与外部互联(跨进程/跨信任域)本质约束不同。
内部先上分布式协议(Outbox/消息队列/序列化)是给简单问题背复杂度。
- 内部协作:补"能力"(任务包/结构化产物/子 Agent 要输入),不补"协议"
- 外部互联:真接外部 Agent 时才上协议层(A2A Adapter + 信任边界)

### 1.4 与 ChatGPT 建议的差异

ChatGPT 建议"现在就内部协议化,未来平滑接外部"(11 张表 + Outbox + AgentExecutor + Registry)。
本规划吸收其**真有价值的部分**(任务包/Artifact/状态机/控制面数据面分离),**暂缓其分布式系统部分**(Outbox/消息队列/Registry/信任边界)。

---

## 2. 三个核心对象(参考 A2A,内部用 domain struct)

参考 A2A,父子 Agent 通讯围绕三个对象,**不传完整聊天历史**:

### 2.1 Task:任务合同(替代裸 goal)

```go
type TaskSpec struct {
    Goal                string            // 目标是什么
    Background          map[string]any    // 背景(项目/技术栈/当前架构)
    Deliverables        []Deliverable     // 必须交付什么
    CompletionCriteria  []string          // 什么情况算完成
    Constraints         Constraints       // 预算和限制(max_steps/deadline)
}
```

关键:goal + deliverables + completion_criteria + constraints。
直接缓解循环不收敛--子 Agent 知道"做到什么程度算完"。

### 2.2 Event:状态变化(异步通知)

```go
type TaskEvent struct {
    EventType string      // task.accepted/running/progress/input_required/completed/failed/cancelled
    TaskID    int64
    Sequence  int        // 事件序号(幂等用)
    Payload   map[string]any
    OccurredAt time.Time
}
```

主 Agent 真正关心的:`input_required` / `completed` / `failed` / `cancelled`。
`progress` 供前端展示,不必每次唤醒主 Agent。

### 2.3 Artifact:结构化产出(替代自由 text)

```go
type Artifact struct {
    ID          int64
    TaskID      int64
    Name        string          // 如 "research_report"
    ContentType string          // "application/json" / "text/markdown"
    SchemaName  string          // 如 "agent.research-report.v1"
    Content     json.RawMessage // 结构化内容
    StorageURI  string          // 大内容存外部(预留)
}
```

主 Agent 按 schema 字段取用,不再解析自由文本。兼容旧 artifact(text 回落)。

---

## 3. 状态机(加 input_required)

```
pending -> running -> completed
                   -> failed
pending/running -> cancelled
running -> input_required -> running(补充后)/failed/cancelled
```

新增 `input_required`:子 Agent 主动要输入(对应 update_task 的反向)。
终态:completed/failed/cancelled(不可重启,要继续就建新任务,关联 parent_task_id)。

---

## 4. 迭代任务清单

### 第一阶段(高优先级·纯能力,现在做)

| # | 任务 | 价值 | 表变更 |
|---|------|------|--------|
| 17 | **任务包**:delegate 传 goal+deliverables+criteria+constraints | 直接缓解循环不收敛 | agent_tasks 加 task_spec JSONB + parent_task_id |
| 18 | **Artifact 结构化**:独立表 + schema | 主 Agent 按字段取用 | 新建 agent_artifacts 表 |
| 19 | **input_required**:子 Agent 主动要输入 | 双向通讯 | 状态机加 input_required |
| 20 | **控制面/数据面分离**:事件带 id,大内容走 Artifact | 省 token | 无新表(逻辑层) |

### 预留(低成本占位)

| # | 任务 | 说明 |
|---|------|------|
| 21 | AgentExecutor 接口 | 只 Local 实现,不写空 Adapter |

### 第二阶段(事件驱动)

| # | 任务 | 触发条件 |
|---|------|---------|
| 22 | 事件流表 + LISTEN/NOTIFY 替代轮询 | 轮询延迟成为痛点时 |

新建 `agent_task_events` + `agent_task_messages`(notes 从 task 内迁出)。
用 Postgres LISTEN/NOTIFY(单进程不需 Outbox,同事务写 task+event 保证原子)。

### 第三阶段(多 Agent)

| # | 任务 | 触发条件 |
|---|------|---------|
| 23 | Agent Registry 能力发现 | 3+ 种 Agent 且需动态选择 |

新建 `agents` + `agent_capabilities`。主 Agent 按能力路由而非写死 type。

### 第四阶段(外部 Agent)

| # | 任务 | 触发条件 |
|---|------|---------|
| 24 | A2A Adapter + 信任边界 | 有真实外部 Agent 需求 |

此时才需要:Outbox(跨进程可靠性)/信任边界(最小披露/脱敏/防注入)/agent_callbacks。
内部协议映射 A2A(Task/Event/Artifact/Message)。

---

## 5. 表设计评估(渐进,非一次性 11 张)

ChatGPT 建议 11 张表(按终局)。本规划**渐进建表**,每张表被真实需求驱动:

| 表 | 阶段 | 理由 |
|----|------|------|
| agent_tasks | 已有,扩字段 | 第一阶段(task_spec/parent_task_id) |
| agent_artifacts | **第一阶段新建** | 结构化 Artifact |
| agent_task_events | 第二阶段 | 事件流 |
| agent_task_messages | 第二阶段 | input_required 消息历史 |
| agents + agent_capabilities | 第三阶段 | Registry |
| agent_callbacks | 第四阶段 | 外部回调 |
| ~~agent_runs~~ | **不建** | task 即 run,无意义拆分 |
| ~~agent_tool_calls~~ | **不建** | agent_steps 已覆盖且更细 |

**不一次建 11 张的理由**:大半表会空着没代码用;schema 按终局锁死但实战会变;有数据后改表要迁移。渐进建表每张表经实战检验。

---

## 6. 设计原则(采纳)

1. Agent 间传任务,不传完整脑内上下文
2. 结果用 Artifact,不只是一段字符串
3. 异步任务通过事件连接(第二阶段后),不通过等待连接
4. 控制面(事件,小)与数据面(Artifact,大)分离
5. 数据库保存事实,模型只负责决策
6. 内部模型可映射 A2A,但不锁死协议版本

## 7. 明确不做(暂缓)

- ❌ Outbox/消息队列(单进程不需要,第四阶段才可能)
- ❌ Agent Registry(一个 Agent 不需要,第三阶段)
- ❌ 外部 Agent 信任边界(没外部 Agent,第四阶段)
- ❌ 11 张表一次建(渐进)
- ❌ 三个空 Adapter(只定义接口,实现按需)

---

## 8. 落地路径

```
第一阶段(现在):任务包 + Artifact + input_required + 控制面分离
    ↓
预留:AgentExecutor 接口(只 Local)
    ↓
第二阶段:事件流(LISTEN/NOTIFY)
    ↓
第三阶段:Agent Registry(多 Agent)
    ↓
第四阶段:A2A + 外部 Agent
```

每个阶段独立可交付,不依赖后续阶段。第一阶段完成后,当前"子 Agent 循环不收敛 + 产物难用"的痛点即缓解。

---

**文档版本**:v1.0
**最后更新**:2026-07-27
**状态**:第一阶段待开发(任务 #17 起步)
