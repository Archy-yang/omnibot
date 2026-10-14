# 21 - 个人经历层技术方案：Personal Journal 与 Topic Archive

> 2026-10-14 立项。目标：把现有记忆底座（M6 Matter 分层 + M7 三段式检索 + 沉淀管线）
> 变成**用户可感知、可纠正的个人经历层**——Journal（时间轴）与 Archive（主题档案）。
> 按 [20-个人管家演进路线图](./20-个人管家演进路线图.md) 第一阶段开工。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案评审中 |
| 上游 | [12-记忆系统技术方案](./12-记忆系统技术方案.md)（沉淀管线/溯源）、[16-架构迭代路线图](./16-架构迭代路线图-V1-对话与上下文演进.md)、20 号路线图第一阶段 |
| 关键判断 | **不新建任何表**——Journal/Archive 是 memories/matters 之上的视图 + 一个时间列 |

---

## 1. 这个文档解决什么问题？

现有记忆层的四个"存而未见"：

1. **无事件时间**：memory 只有 `created_at`（沉淀时间）。"上周去爬的山"今天才被沉淀，时间轴上会排错位置——Journal 需要的是**事件发生时间**。
2. **碎片形态**：memories 是 Agent 检索用的扁平 fact 列表，没有按时间组织、可浏览可纠正的产品形态。
3. **Matter 无用户入口**：Matter（主题档案）只有 Agent 侧检索（`SearchMatters`），无任何 Web API；`StateDesc`（覆写式最新状态）用户看不到也改不了。
4. **溯源链路存而未见**：`memory_message_links`（memory↔message 多对多，12 号 §7.3）落库了，但没有任何 API 透出"这条记忆来自哪些对话"。

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| Journal/Archive 是否新建表 | **不新建**，视图 over memories/matters | 数据与沉淀管线已在；新表必然双写漂移 |
| 时间轴锚点 | memories **新增 `happened_at` 列**，沉淀时 LLM 抽取，缺省 NULL | `created_at` 是沉淀时间≠事件时间；NULL 展示时回落 `created_at`（语义仍正确：起码"这件事被谈起"是那天） |
| Archive 演进时间线 | 不加表：档案时间线 = 挂靠 memories 按 `happened_at` 排序 + 溯源 | M6 的 `StateDesc` 覆写式只留最新状态，历史脉络本来就在挂靠记忆里 |
| 用户纠正入口 | 复用现有 memory PUT/DELETE；**新增 matter 的 StateDesc/Status 编辑口** | 最小改动；12 号已有记忆 CRUD |
| 禁止沉淀（黑名单） | v1 不做 | 编辑/删除已覆盖"纠正"诉求；黑名单属召回/注入侧治理，与 22 号 Recall 同批更合适 |
| matter 用户可建/删/改名 | **不开放** | title 是 (user_id, title) 唯一键 + Agent 归档轴，用户改名会裂档案；档案由沉淀管线自然生长，删除牵连挂靠记忆语义复杂 |

## 2. 读之前你需要知道什么？

- 12 号 §7 沉淀管线：区间对话 → LLM 纪要 → memories 落库（M8.4 起 episode 断源，归一为 fact/loop 两层）
- M6/M8：Matter=主题档案（`StateDesc` 覆写式、`Status` active/done/archived），memory 可选挂靠 `MatterID`
- 12 号 §7.3 溯源：`memory_message_links` 多对多，auto 记忆建链时校验消息在沉淀区间内；manual 无链
- 现有 Web API：`/api/v1/memories` CRUD + pin + loop-status（memory_handler.go），**Matter 无 API**

## 3. 数据模型改动（唯一一处）

```go
// internal/domain/memory/memory.go — Memory 新增:
HappenedAt *time.Time `gorm:"index:idx_user_happened,priority:2"` // 事件发生时间(NULL=未识别,展示回落 CreatedAt)
// UserID 已有索引;联合索引 (user_id, happened_at) 服务时间轴分页
```

- **存量数据不回填**：NULL → 展示回落 `created_at`，语义正确且免迁移脚本。
- matters 表零变更。

## 4. 沉淀管线改动：happened_at 抽取

- `digest_prompts`：fact/loop 抽取输出增加 `happened_at` 字段——识别**事件发生时间**（对话中提到的日期/相对时间如"上周三"），以沉淀区间锚点换算为绝对时间；无明确时间则留空。
- `digest_extract` 解析容错：非法日期 → NULL；**超出 [区间起点-1年, now+1天] 的值 → NULL**（防模型幻觉未来日期）。
- manual 记忆（用户手写）不抽取，恒 NULL。
- 沉淀既有测试零回归（新增字段为增量输出，旧提示词路径兼容）。

## 5. API

### 5.1 Journal 时间轴

```
GET /api/v1/memories/journal?kind=all|fact|loop&from=<RFC3339>&to=<RFC3339>&cursor=&limit=
```

```json
{ "success": true, "data": { "items": [
  { "id": 12, "content": "...", "kind": "fact", "source": "auto", "pinned": false,
    "happened_at": "2026-10-01T00:00:00Z",   // NULL 时不返回,前端回落 created_at
    "created_at": "...", "matter_id": 3,
    "links": [ { "message_id": 881, "occurred_at": "...", "snippet": "前80字…" } ] } ],
  "next_cursor": "..." } }
```

- 排序：`happened_at` DESC，NULL 排队尾按 `created_at`；cursor 分页（`happened_at,id` 复合键）。
- 溯源拼装：links join messages 取片段（前 80 字）与消息时间；**消息已不存在（清理/compact）→ 该 link 静默跳过**，不报错。
- 挂靠 matter 的条目同时返回 `matter_id`（前端跳档案）。

### 5.2 Archive 档案

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/matters?status=active` | 列表：id/title/state_desc/status/updated_at/挂靠记忆数 |
| GET | `/api/v1/matters/:id` | 详情 + 时间线：matter 元信息 + 挂靠 memories 按 happened_at ASC（同 5.1 条目形状）+ pinned 前置 |
| PUT | `/api/v1/matters/:id` | 用户纠正口：`{state_desc?, status?}`——只此两字段；title 不可改（§1.1） |

- 全部 JWT 归属校验（他人 404，同 memory handler 现惯例）。

## 6. 前端

沿用抽屉范式：新增 **JournalDrawer**（导航入口与 MemoryDrawer 并列），内部双 tab：

- **时间轴**：Journal 按月分组的时间流；条目显示内容/事件时间（无则显示沉淀时间并标注"沉淀于"）/kind 徽标/所属档案跳转；点开条目 → 溯源消息片段 → 编辑/删除（复用现有 memory API）。
- **档案**：matter 卡片列表（title + 当前状态 + 记忆数）→ 点开详情时间线；StateDesc/Status 可编辑（纠正口）。

MemoryDrawer（碎片管理）保留不动；pinned 逻辑不受影响。

## 7. 测试计划（TDD）

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | 管线 | happened_at 抽取 | 合法日期落列；相对时间换算；非法/超界 → NULL；manual 恒 NULL |
| 2 | API | journal | 分页排序（NULL 尾部）/kind·时间过滤/溯源片段拼装/消息缺失静默跳过/他人 404 |
| 3 | API | matters | 列表过滤与计数/详情时间线排序（pinned 前置）/PUT 仅允许 state_desc·status/title 不可改/他人 404 |
| 4 | 回归 | 沉淀管线既有测试 | 不动全绿 |

## 8. 风险

| 风险 | 处置 |
|------|------|
| LLM 时间抽取质量不稳 | 严格容错（非法即 NULL 回落）+ 抽样审计记入 20 号 §7 的质量验证清单；坏数据可经 UI 纠正 |
| 大时间轴分页性能 | (user_id, happened_at) 索引 + cursor 分页；时间轴天然按用户隔离 |
| 溯源消息被清理 | join miss 静默降级（§5.1），不阻塞条目展示 |

## 9. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| J1 | `happened_at` 列 + 管线抽取与容错 | §3 §4 + 测试 1/4 |
| J2 | journal API + matters 三端点 | §5 + 测试 2/3 |
| J3 | JournalDrawer（时间轴 + 档案 + 纠正） | §6 |

后续 22 号（Personal Recall 时间/主题维度、沉淀黑名单）承接本方案未覆盖项。

## 10. 边界与不做

- 不新建表、不做双写；Journal/Archive 是**长期对话之上的视图**，不引入会话概念。
- matter 不开放用户创建/删除/改名。
- 禁止沉淀黑名单、回忆问答式 Recall → 22 号。
- Journal 不做富文本/多媒体，v1 纯文本条目。

---

**文档版本**：v1.1
**创建日期**：2026-10-14

---

## 落地记录（J1 + J2 + J3，2026-10-14）

- **J1**：`memories.happened_at` 列 + `(user_id, happened_at)` 索引（AutoMigrate,存量不回填）；
  沉淀 facts schema 增加 `happened_at`（提示词明令"对话没提就留空,不要编造"）；
  `parseHappenedAt` 严格容错——空/垃圾/相对时间/超窗 [区间起点-1年, 区间末+24h]（按日历日对齐,
  日期精度 00:00 不卡带时刻的下界）一律 NULL；冲突更新路径不改写既有时间。
- **J2**：仓储 `ListJournal`（有效时间=`COALESCE(happened_at,created_at)` 降序,游标谓词 OR 展开,
  SQLite/PG 通吃）+ `ListLinksForMemories`/`CountByMatter`/`ListByMatter`（pinned 前置,有效时间升序）
  + matter `GetByID`/`ListByUserID`/`UpdateFields`；service `Journal`（溯源拼装,片段 80 字,
  已消失消息静默缺失,游标 base64 不透明串）+ `Matters`/`MatterDetail`/`UpdateMatter`（仅
  state_desc/status,title 不可改,未命中一律 ErrMatterNotFound 防存在性泄露）；API
  `GET /memories/journal` + `GET/PUT /matters(/:id)`,JWT 归属隔离。
- **J3**：`JournalDrawer` 双 tab（时间轴按月分组/kind 过滤/翻页/条目展开溯源/编辑删除复用记忆
  CRUD/跳档案 + 档案列表/详情时间线/纠正口）,侧栏新增「经历」导航,抽屉内部状态不进 Store。
- 测试：解析 14 断言/对账 3 场景/提示词守护 + 仓储 5 项/service 5 项/handler 6 项,
  全量 39 包绿；vue-tsc 干净,vite build 通过；`happened_at` 列与索引 DB 实查确认。
