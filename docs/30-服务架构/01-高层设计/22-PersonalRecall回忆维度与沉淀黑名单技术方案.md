# 22 - Personal Recall 技术方案：回忆的时间/主题维度 与 沉淀黑名单

> 2026-10-14 立项。目标：①让 Agent 能按**时间与主题**准确回忆——"上个月做了什么""上次为什么
> 这么决定"能给出带时间线与原始对话依据的回答；②让用户能**禁止某些内容长期沉淀**。
> 承接 [21-个人经历层](./21-个人经历层Journal与Archive技术方案.md) 边界声明（21 号 §10）。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案评审中 |
| 上游 | [12-记忆系统](./12-记忆系统技术方案.md) §8（三段式检索）、M7（中期记忆）、[21-个人经历层](./21-个人经历层Journal与Archive技术方案.md)（happened_at/Journal）、[20-路线图](./20-个人管家演进路线图.md) 第一阶段 |
| 关键判断 | 三段式检索是**内存打分**（全量载入后逐条算分）——时间/主题过滤加在打分循环里即可，**仓储零改动** |

---

## 1. 这个文档解决什么问题？

现状四个缺口：

1. **无时间维度**：`search_memories` 只有语义/子串打分，"最近一个月做了什么"这类时间约束
   只能靠语义相似度碰运气；输出标注也只有 `created_at`（21 号刚落地的 `happened_at` 未被检索侧使用）。
2. **无主题维度**：不能"只看旅行这个档案里的记忆"。
3. **无黑名单**：用户说"别记 XX"，系统没有任何机制承接——沉培养管线照记、注入照带。
4. **输出非时间线**：检索结果按相关度排序，回答"这段时间做了什么"时应按时间排序呈现。

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 新工具 vs 扩展现有工具 | **扩展 `search_memories`**（可选 `from`/`to`/`matter_title` 参数），不加新工具 | 工具数量克制；时间换算（"上个月"→RFC3339）是 LLM 的活（已有 get_current_time 配合），传参即可；prompt 不膨胀 |
| 时间过滤的锚 | **有效时间** = `COALESCE(happened_at, created_at)`（与 Journal/21 号同锚） | 一套时间语义贯穿沉淀/视图/检索，"上周爬的山"按上周被回忆到 |
| 时间过滤的实现 | 打分循环内 skip（service 层内存过滤） | 三段式本就全量载入打分，加一行判断；仓储零改动 |
| 时间线输出 | 带 `from`/`to` 时，命中结果按有效时间**升序**重排输出 | "这段时间做了什么"的阅读顺序是时间顺序；不带时间参数时保持相关度排序（原行为） |
| 黑名单的语义边界 | **只拦未来**（沉淀过滤 + 注入过滤）；建规则时**同时删除存量匹配条目**并回报条数 | "别记 X"的直觉语义是"以后别记，已有的也忘掉"；只拦不删会留一半，用户困惑 |
| 黑名单的粒度 | 关键词/短语包含匹配（大小写不敏感），v1 不做语义/正则 | 包含匹配可预期、可解释；语义黑名单误杀率高，等真需求 |
| 黑名单的入口 | 工具 `manage_memory_rules`（对话内）+ MemoryDrawer 管理面（Web） | 对话内"别记 XX"顺手即用；管理面兜底查看/清理 |

## 2. 读之前你需要知道什么？

- 12 号 §8 检索：`SearchMemories`（记忆语义+子串融合）、`SearchMatters`（事项全景）、M7 `SearchRecentMessages`（消息向量+时间加权）；三者由工具层 `search_memories` 编排为三段式输出
- 21 号：`memories.happened_at`（事件时间，NULL 回落 created_at）、Journal 时间轴
- 沉淀管线：`applyFacts` 候选过滤 → 嵌入 → 去重裁决 → 落库（digest_reconcile.go）；注入侧：`GetRecentForContext` + 常驻注入（pinned/manual）

## 3. 检索维度增强（R1：service 层）

```go
// MemoryFilter 检索过滤(21号有效时间语义):nil 字段不过滤
type MemoryFilter struct {
    From     *time.Time // 有效时间下界(含)
    To       *time.Time // 有效时间上界(含)
    MatterID *int64     // 只看某档案
}
```

- `SearchMemories(ctx, userID, query, topK, filter)`：打分循环内，`effectiveTime(m)` 不在 [From,To] → skip；MatterID 非 nil → `MatterID != filter.MatterID` skip。签名变更为五参（编译器强制更新全部调用方，无静默兼容负担）。
- `SearchRecentMessages`：回表后按 `CreatedAt` 窗口过滤（中期层语义是"消息发生时间"）。
- `SearchMatters` 不加时间过滤（事项是"当前状态"语义，时间窗无意义）；MatterID 过滤亦不需要（事项本身就是主题）。
- 既有调用方（tool 工具层）全部显式传 `nil`/构造 filter，行为不变。

## 4. 工具层（R1：search_memories 增强）

```
search_memories(query, from?, to?, matter_title?)
  - from/to: RFC3339。"上个月""去年十月"等相对时间由你先换算(配合 get_current_time),再传绝对时间
  - matter_title: 档案标题,精确匹配 ListByTitle;不存在时返回可读提示
  - 行为:传了 from/to → 命中结果按有效时间升序输出为时间线(每条标注事件日期);
         否则保持相关度排序(现行为)
```

- `memoryAnnotation` 改为 **happened_at 优先**、回落 created_at（与 Journal 展示语义一致）。
- matter_title 解析失败（无此档案）→ 不报错，输出"未找到档案「X」"提示让 LLM 换查询。

## 5. 沉淀黑名单（R2）

### 5.1 数据模型

```go
// internal/domain/memory/blacklist.go — TableName: memory_blacklists
type MemoryBlacklist struct {
    ID        int64     `gorm:"primaryKey;autoIncrement"`
    UserID    int64     `gorm:"index;not null"`
    Pattern   string    `gorm:"size:128;not null"` // 关键词/短语,包含匹配(大小写不敏感)
    CreatedAt time.Time `gorm:"not null"`
    // 同用户内 Pattern 唯一(防重复规则);删除 = 解除禁止
}
```

### 5.2 执行点（三处）

| 执行点 | 行为 |
|--------|------|
| 沉淀管线 `applyFacts` | 候选 content 命中任一规则 → **静默 skip**（记 debug 日志；管线视作"无此候选"，水位照常推进） |
| 注入侧（`GetRecentForContext` / 常驻注入组装） | 命中规则的记忆条目**不注入**（历史存量兜底，即使建规则时未删净） |
| 检索侧（`SearchMemories`） | 命中规则的条目**不返回**（Agent 不应把用户已禁止的内容再翻出来） |
| Journal / MatterDetail 视图 | **不过滤**——管理视图要可见真相，用户可自行删除条目（21 号纠正口） |

### 5.3 规则管理

- Service：`ListBlacklist` / `AddBlacklist(userID, pattern)`（去重、trim、≤128 字）+ **删存量**：加入时删除该用户所有 content 包含 pattern 的记忆（含 links 级联），返回删除条数；`RemoveBlacklist(userID, id)`（解除后不恢复已删条目——语义是"以后可以记"，不是"找回"）。
- 工具 `manage_memory_rules`：`action: add|list|remove`；描述引导：用户表达"别记/忘掉/以后不要记 XX"时 add，"又可以记 XX 了"时 remove；add 返回文案含"已删除 N 条存量记录"。
- Web：MemoryDrawer「自动沉淀的」tab 底部新增"沉淀黑名单"折叠区（列表 + 添加 + 删除；add 同样回报存量删除数）。

## 6. 测试计划（TDD）

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Service | SearchMemories filter | 时间窗内外取舍/边界含端点/MatterID 隔离/nil filter 行为同现状 |
| 2 | Service | SearchRecentMessages 时间窗 | 窗外消息被滤除 |
| 3 | Tool | search_memories 参数 | from/to 传递与时间线升序输出/matter_title 命中与未命中提示/无参行为同现状 |
| 4 | Service | Blacklist | add 去重+删存量计数/remove/注入与检索命中过滤/管线 applyFacts skip |
| 5 | Tool | manage_memory_rules | add/list/remove 契约;add 文案含删除条数 |
| 6 | Web | MemoryDrawer 黑名单区 | vue-tsc + 手动冒烟 |
| 7 | 回归 | 三段式既有测试 | 不动全绿 |

## 7. 风险

| 风险 | 处置 |
|------|------|
| LLM 换算相对时间出错 | 换算是 LLM 强项且有 get_current_time 配合；错误窗口最多返回空结果，提示重试 |
| 黑名单误删（关键词太宽） | add 前工具返回"将删除 N 条匹配记录"？——不做确认流（对话流二次确认体验差），靠规则粒度提示（描述里写明"尽量用具体词"）+ Journal 可重建认知 |
| 五参签名波及调用方 | 编译器强制；调用方仅工具层与管线测试，量小 |

## 8. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| R1 | 检索维度增强 + search_memories 参数 + 时间线输出 | §3 §4 + 测试 1~3/7 |
| R2 | 黑名单（表/管线/注入/检索/工具/Web） | §5 + 测试 4~6 |

## 9. 边界与不做

- 不做语义/正则黑名单；不做黑名单恢复已删条目。
- 不做独立"回忆页"UI——回忆走对话（Agent 回答带时间线与依据），浏览走 21 号 Journal。
- SearchMatters 不做时间过滤（事项是当前状态语义）。

---

**文档版本**：v1.0
**创建日期**：2026-10-14
