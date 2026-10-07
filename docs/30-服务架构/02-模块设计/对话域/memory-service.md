# 长期记忆服务

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.3+ |
| 最后更新 | 2026-06-13 |
| 状态 | ✅ 已实现 |

---

## 1. 这个服务解决什么问题？

**核心问题**：
1. 短期上下文记忆只能覆盖最近 10 轮对话，用户切换话题后之前的偏好和背景全丢失
2. 用户每次开始新对话都需要重复说明自己的偏好、项目背景
3. 没有用户可控的长期记忆能力，后续自动记忆提取、向量召回都缺少基础

**解决方案**：
> ⚠️ **2026-10-07 追平声明（架构复评 P0-2）**：本文档曾停在 2026-06 MVP 基线，与 12 号技术方案
> （M6/M7/M8 落地后的现行口径）存在多处冲突。本次已追平实体表/业务规则/Repository 接口/决策记录；
> 若仍有出入，**以《12-记忆系统技术方案》为唯一权威**。

长期记忆服务提供入口无关的记忆管理能力：用户通过显式命令保存长期记忆；注入走分层
（manual 全量 ∪ 置顶自动常驻，其余自动记忆走 search_memories 工具检索）。

读之前你需要知道：
- [消息与上下文记忆服务](./message-service.md)
- [用户核心服务](../用户域/user-service.md)
- 产品 PRD：`docs/20-产品PRD/in_progress/长期记忆MVP-PRD-v1.3.md`

---

## 2. 核心领域模型

### Memory 实体

| 字段 | 类型 | 说明 |
|------|------|------|
| ID | int64 | 主键，自增 |
| UserID | int64 | 所属用户 ID，索引 |
| Content | string | 记忆文本内容 |
| Source | string | manual=用户交代 / auto=沉淀管线提取（注入分层） |
| Kind | string | 分层：fact/loop（episode 为历史兼容值，M8.4 起断源归一 fact） |
| LoopStatus | string | 仅 kind=loop：open/closed（M8.2 生命周期） |
| SourceMessageID | *int64 | 溯源：沉淀自哪条消息 |
| MatterID | *int64 | 挂靠事项（M6 matters 机制） |
| Embedding | []float32 | 语义向量（JSON 列 + 应用层余弦，§6.2） |
| EmbeddingModel | string | 向量所属模型（异构向量不可比） |
| Category / Importance | - | 预留列，暂未启用 |
| Pinned / PinnedAt | bool/*time.Time | M8.3：置顶常驻 core |
| CreatedAt / UpdatedAt | time.Time | 时间戳 |

### 业务规则

- 每条记忆属于一个用户，不同用户完全隔离
- 单条内容最大 200 个 Unicode 字符
- 按创建时间排序（`ORDER BY id ASC`）
- 上下文注入走分层：manual 全量常驻（用户意志，不参与预算截断）∪ 置顶自动记忆（预算内按 pinned_at DESC）；其余自动记忆只出存在性提示（§6.5 修订 + §14.2.4）
- 未决 loop（kind=loop 且 open）进世界观快照，管理面可手动关闭/重开（E3：`PUT /memories/:id/loop-status`）

---

## 3. 服务接口

```go
type MemoryService interface {
    Remember(ctx, userID, content string) (*Memory, error)
    List(ctx, userID int64) ([]*Memory, error)
    Clear(ctx, userID int64) error
    Delete(ctx, userID, memoryID int64) (bool, error)
    Update(ctx, userID, memoryID int64, content string) (*Memory, error)
    GetRecentForContext(ctx, userID int64, limit int) ([]string, error)
}
```

### Remember — 保存记忆

```
输入：用户 ID、记忆文本
输出：保存后的 Memory 对象

1. TrimSpace 处理前后空白
2. 空内容 → 返回 ErrEmptyContent
3. 超过 200 字 → 返回 ErrContentTooLong
4. 创建 Memory 实体 → 调用 Repository.Create
5. 日志只记录 user_id、memory_id、content_length
```

### List — 列出记忆

返回当前用户全部记忆，按创建时间正序。无记忆时返回空列表。

### Clear — 清空全部记忆

删除当前用户所有记忆。幂等：无记忆时也返回成功。

### Delete — 删除单条记忆

按 `memoryID + userID` 双重校验删除。返回 `(true, nil)` 表示删除成功，`(false, nil)` 表示不存在或无权限。

### Update — 更新单条记忆

与 Remember 相同的校验规则（trim、空内容、超长）。按 `memoryID + userID` 双重校验更新。返回更新后的 Memory 对象，不存在时返回 nil。

### GetRecentForContext — 获取上下文用记忆

返回最近 N 条记忆的纯文本内容列表，供 `MessageService.BuildContextMessages` 构造 system message。返回顺序从旧到新。

---

## 4. Repository 接口

```go
type MemoryRepository interface {
    Create(memory *Memory) error
    ListByUserID(userID int64) ([]*Memory, error)
    ListManualByUserID(userID int64) ([]*Memory, error)          // 常驻注入(手动层)
    CountByUserIDAndSource(userID int64, source string) (int64, error)
    DeleteByUserID(userID int64) error
    DeleteByUserIDAndSource(userID int64, source string) error   // 双 tab 各清各的
    GetByID(id, userID int64) (*Memory, error)
    DeleteByID(id, userID int64) (bool, error)
    UpdateContentByID(id, userID int64, content string) (*Memory, error)
    GetRecentByUserID(userID int64, limit int) ([]*Memory, error)
    UpdateContentEmbeddingByID(id, userID int64, content string, embedding []float32, model string) error // §7.3 冲突更新
    CreateLinks(links []MemoryMessageLink) error                 // M5.2 溯源
    ReplaceLinksForMemory(memoryID int64, messageIDs []int64) error
    ListByUserIDAndMatter(userID, matterID int64) ([]*Memory, error) // M6.2 事项全景
    ListOpenLoops(userID int64, limit int) ([]*Memory, error)    // M8.2 未决 loop(两端取样)
    TransitionLoopStatus(id, userID int64, from, to string) (bool, error) // CAS 迁移
    ListPinnedAutoByUserID(userID int64) ([]*Memory, error)      // M8.3 置顶常驻
    SetPinned(id, userID int64, pinned bool) (bool, error)
}
```

> 完整签名以 `internal/repository/memory/memory_repo.go` 为准。

### 查询规则

| 方法 | SQL 策略 |
|------|---------|
| `ListByUserID` | `WHERE user_id = ? ORDER BY id ASC` |
| `GetRecentByUserID` | `WHERE user_id = ? ORDER BY id DESC LIMIT ?`，再反转为 ASC |
| `GetByID` | `WHERE id = ? AND user_id = ?`，不存在返回 nil |
| `DeleteByID` | `WHERE id = ? AND user_id = ? DELETE`，返回 `RowsAffected > 0` |
| `UpdateContentByID` | `WHERE id = ? AND user_id = ? UPDATE`，同时更新 `updated_at` |

所有操作都通过 `user_id` 保证用户隔离。

---

## 5. 上下文注入流程

```
用户发送普通消息
    ↓
MessageService.BuildContextMessages 被调用
    ↓
通过 LongTermMemoryProvider 接口调用 GetRecentForContext(userID, 10)
    ↓
┌─ 有记忆 → 构造 system message
│   "以下是用户长期记忆，请在回答时自然参考..."
│   1. 我偏好简洁直接的回答
│   2. 我正在开发 OmniBot 项目
└─
    ↓
┌─ 无记忆 → 跳过，不注入
└─
    ↓
┌─ 查询失败 → 记录 ERROR 日志，降级为无长期记忆
└─
    ↓
继续构建短期上下文 messages
```

### 窄接口设计

`internal/service/chat` 定义了 `LongTermMemoryProvider` 窄接口：

```go
type LongTermMemoryProvider interface {
    GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}
```

聊天模块只依赖这一个方法，不依赖完整 `MemoryService`。好处：
- 接口最小化，chat 层不需要知道记忆的增删改
- `MemoryService` 后续扩展不影响聊天模块
- 测试 mock 更简洁

---

## 6. 多通道接入

长期记忆能力设计为入口无关：

| 通道 | 记忆管理方式 | 上下文注入 |
|------|------------|-----------|
| 微信公众号 | `#记住` / `#我的记忆` / `#清空记忆` / `#删除记忆 N` 命令 | 自动（通过 MessageService） |
| Web | `GET/POST/DELETE /api/v1/memories` + `DELETE/PUT /api/v1/memories/:id` | 自动（通过 MessageService） |
| 飞书/钉钉（未来） | 复用同一 MemoryService | 自动 |

同一用户在不同通道的记忆完全共享。

---

## 7. 安全与日志

| 规则 | 说明 |
|------|------|
| 日志不记录完整内容 | 只记录 user_id、memory_id、content_length、operation |
| 用户隔离 | 所有查询按 user_id 过滤 |
| 对外错误文案 | 统一返回"服务暂时不可用，请稍后再试"，不暴露内部错误 |
| 安全提示 | 保存记忆时提醒用户不要保存密码、API Key、身份证号 |

---

## 8. 设计决策记录

### 决策1：为什么用窄接口而不是完整依赖？

- **背景**：chat 层只需要 `GetRecentForContext`，但最初依赖了完整 `MemoryService`
- **决策**：定义 `LongTermMemoryProvider` 窄接口
- **原因**：
  - 接口隔离原则：chat 层不应知道记忆的增删改
  - `MemoryService` 增加 Delete/Update 方法时不影响 chat 层
  - 测试 mock 更简洁，只需实现一个方法

### ~~决策2：为什么不加 embedding 字段？~~（已作废，2026-10-07 标注）

- **作废原因**：`Embedding` 列在记忆 M1 落地时即已加入（JSON 列 + 应用层余弦，
  非 pgvector，见 12 号 §6.2）；M7 更建了消息级向量层。MVP 时期的"暂不加"决策
  已被实现全面超越，原文保留会造成误导。

### 决策3：为什么按序号删除而不是按 ID？

- **背景**：微信聊天场景中用户看不到 ID，只能看到 `#我的记忆` 返回的序号列表
- **决策**：微信命令用序号，Web API 用 ID
- **原因**：
  - 微信文本交互天然适合序号
  - Web API 有完整 UI，可以直接操作 ID
  - 两种方式最终都落到 `DeleteByID`

---

## 9. 测试覆盖

| 测试层级 | 测试内容 |
|---------|---------|
| Domain | `NewMemory` 设置字段、`TableName()` 返回 `memories` |
| Repository | 创建、按用户列出、用户隔离、清空、最近 N 条、按 ID 获取/删除/更新 |
| Service | Remember trim/空/超长、List/Clear 行为、Delete/Update 成功/不存在/错误 |
| WeChat Handler | `#记住`/`#我的记忆`/`#清空记忆`/`#删除记忆` 命令及边界 |
| Web Handler | GET/POST/DELETE/PUT /memories 端点及校验 |

---

## 10. 下一步演进

> **已全部落地（2026-10 更新）**：三层记忆、语义检索、自动沉淀、Turn/Compact/Recall、
> loop 生命周期（M8.2）、pinned 常驻 core（M8.3）、episodes 断源（M8.4）均已上线，
> 设计与落地进度以《[12-记忆系统技术方案](../01-高层设计/12-记忆系统技术方案.md)》
> 与《[16-架构迭代路线图](../01-高层设计/16-架构迭代路线图-V1-对话与上下文演进.md)》为准。

- ~~v1.5：自动从对话中提取长期记忆 + embedding 向量召回~~ → 已由 12-记忆系统技术方案承接（三层记忆 + EmbeddingProvider）
- v1.4/v1.6 中的记忆分类、去重合并机制随沉淀管线后续迭代
- v2.0：跨通道记忆完全打通
