# 长期记忆 MVP 技术设计

## 背景

长期记忆 MVP 基于 `docs/20-产品PRD/in_progress/长期记忆MVP-PRD-v1.3.md`，目标是让用户通过显式命令保存、查看、清空长期记忆，并在后续普通对话中自动使用最近 10 条长期记忆。

本设计采用方案 B：新增长期记忆领域能力，并由现有 `MessageService.BuildContextMessages` 统一组合长期记忆和短期上下文。

## 目标

1. 支持微信公众号入口的长期记忆命令：`#记住 xxx`、`#我的记忆`、`#清空记忆`。
2. 普通对话时自动注入当前用户最近 10 条长期记忆。
3. 保持长期记忆能力入口无关，后续 Web/API/飞书等入口可复用同一 Service。
4. 长期记忆查询失败时降级为无长期记忆对话，不影响普通回复。
5. 日志不输出完整记忆内容。

## 非目标

1. 不做自动记忆提取。
2. 不做向量召回或 pgvector 字段。
3. 不做分类、重要性评分、置顶、过期。
4. 不做单条编辑。
5. 不做敏感信息强识别。

## 架构

新增长期记忆独立领域能力：

```text
internal/domain/memory
  ↓
internal/repository/memory
  ↓
internal/service/memory
  ↓
internal/service/chat.MessageService
  ↓
internal/api/wechat.Handler
```

职责划分：

- `domain/memory`：定义长期记忆实体和基础构造方法。
- `repository/memory`：负责长期记忆持久化查询。
- `service/memory`：负责命令级业务规则、长度校验、列表、清空、上下文召回。
- `service/chat.MessageService`：继续负责短期上下文构建，并组合长期记忆 system message。
- `api/wechat.Handler`：负责微信命令识别与响应，不直接访问数据库。
- `api/routes.go`：负责依赖装配。

## 数据模型

新增 `memories` 表。

```go
type Memory struct {
    ID        int64     `gorm:"primaryKey;autoIncrement"`
    UserID    int64     `gorm:"index;not null"`
    Content   string    `gorm:"type:text;not null"`
    CreatedAt time.Time `gorm:"not null"`
    UpdatedAt time.Time `gorm:"not null"`
}

func (Memory) TableName() string {
    return "memories"
}
```

模型规则：

- 每条记忆属于一个 `UserID`。
- `Content` 保存用户显式输入的文本。
- 单条内容最大 200 个 Unicode 字符，由 Service 层校验。
- `UserID` 建索引，用于用户隔离和按用户查询。
- 不加唯一约束，允许用户保存相同内容。
- 不包含 embedding 字段，避免提前引入向量召回实现。
- `internal/db/database.go` 的 `autoMigrate` 注册 `&memory.Memory{}`。

## Repository 设计

新增 `internal/repository/memory` 包。

```go
type MemoryRepository interface {
    Create(memory *memory.Memory) error
    ListByUserID(userID int64) ([]*memory.Memory, error)
    DeleteByUserID(userID int64) error
    GetRecentByUserID(userID int64, limit int) ([]*memory.Memory, error)
    GetByID(id int64, userID int64) (*memory.Memory, error)
    DeleteByID(id int64, userID int64) (bool, error)
    UpdateContentByID(id int64, userID int64, content string) (*memory.Memory, error)
}
```

查询规则：

- `ListByUserID` 使用 `WHERE user_id = ? ORDER BY id ASC`。
- `DeleteByUserID` 使用 `WHERE user_id = ? DELETE`，只删除当前用户记忆。
- `GetRecentByUserID` 使用 `ORDER BY id DESC LIMIT ?` 获取最近 N 条，再反转为 `id ASC`，保证上下文注入从旧到新。

Repository 不做内容长度校验，不记录日志，不处理微信命令文案。

## Memory Service 设计

新增 `internal/service/memory` 包。

```go
const MaxMemoryContentLength = 200

type MemoryService interface {
    Remember(ctx context.Context, userID int64, content string) (*memory.Memory, error)
    List(ctx context.Context, userID int64) ([]*memory.Memory, error)
    Clear(ctx context.Context, userID int64) error
    GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
    Delete(ctx context.Context, userID int64, memoryID int64) (bool, error)
    Update(ctx context.Context, userID int64, memoryID int64, content string) (*memory.Memory, error)
}
```

错误类型：

- `ErrEmptyContent`：`#记住` 后没有内容。
- `ErrContentTooLong`：内容超过 200 个 Unicode 字符。

业务规则：

- `Remember` 对内容执行 `strings.TrimSpace`。
- 空内容不保存，返回 `ErrEmptyContent`。
- 超长内容不保存，返回 `ErrContentTooLong`。
- 成功保存后日志只允许记录 `user_id`、`memory_id`、`content_length`、`operation`。
- `List` 返回当前用户全部记忆。
- `Clear` 幂等，当前用户没有记忆时仍返回成功。
- `GetRecentForContext` 返回字符串列表，供 `MessageService` 构造 system message。

## Message Service 集成

`internal/service/chat.MessageService` 通过窄接口 `LongTermMemoryProvider` 依赖长期记忆能力，避免依赖完整 `MemoryService`：

```go
type LongTermMemoryProvider interface {
    GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}
```

构造函数保持简单：

```go
func NewMessageService(msgRepo chat.MessageRepository, optionalServices ...interface{}) MessageService
```

当传入 `LongTermMemoryProvider` 时，`BuildContextMessages` 会先查询长期记忆，再查询短期消息。

普通对话的 messages 顺序为：

```text
system prompt
long-term memory system message
recent short-term messages
current user message
```

其中 `system prompt` 仍由 `wechat.Handler.callLLM` 添加。`BuildContextMessages` 返回：

```text
long-term memory system message
recent short-term messages
current user message
```

长期记忆 system message 内容格式：

```text
以下是用户长期记忆，请在回答时自然参考，不要主动提及“我参考了记忆”：

1. 我偏好简洁直接的回答
2. 我正在开发 OmniBot 项目
```

降级规则：

- 长期记忆查询失败时，记录错误日志，不包含记忆内容。
- 查询失败不向 Handler 返回错误。
- 继续使用短期上下文和当前用户消息。
- 短期上下文查询失败时，沿用现有降级策略。

## 微信 Handler 集成

`wechat.Handler` 新增可选 `MemoryService` 依赖。

命令识别顺序：

1. LLM 配置命令。
2. 长期记忆命令。
3. 普通对话。

新增方法：

```go
func (h *Handler) handleMemoryCommand(userID int64, content string) (string, bool)
```

命令规则：

- 输入先 `strings.TrimSpace`。
- `#记住 xxx`：折叠命令后的前导空白，保存 `xxx`。
- `#记住`：返回示例提示。
- `#我的记忆`：返回全部记忆或空状态。
- `#清空记忆`：清空当前用户全部长期记忆。
- `#删除记忆 N`：按序号删除第 N 条记忆。

微信返回文案与 PRD 保持一致。

如果 `MemoryService` 不存在，记忆命令不应被标记为已处理，避免在未装配服务时误吞用户消息。

命令消息保存策略：沿用现有配置命令行为，保存用户命令和助手回复到短期上下文。

## 依赖装配

`internal/api/routes.go` 新增装配：

1. `memoryRepo := memoryRepo.NewMemoryRepository(dbConn.GetGormDB())`
2. `memorySvc := memoryService.NewMemoryService(memoryRepo)`
3. `msgSvc := chatService.NewMessageService(msgRepo, memorySvc)`
4. `wechat.NewHandler(..., llmConfigSvc, msgSvc, memorySvc)`

Web handler 暂不暴露记忆管理入口，但如果它继续通过同一个 `msgSvc` 构建上下文，后续可自然获得长期记忆注入能力。

## 日志与安全

必须遵守：

- 不记录完整记忆内容。
- 不记录完整用户对话内容。
- 保存记忆成功只记录 user_id、memory_id、内容长度、操作类型。
- 查询和清空失败只记录 user_id、操作类型、错误类型。
- 对用户返回统一文案：`服务暂时不可用，请稍后再试`。

实现时应同步检查微信消息处理中的完整消息体日志，避免长期记忆命令内容被原始日志泄露。

## 测试策略

所有实现按 TDD 执行。

### Domain 测试

- `NewMemory` 设置 `UserID`、`Content`、`CreatedAt`、`UpdatedAt`。
- `TableName()` 返回 `memories`。

### Repository 测试

- 创建记忆后可按用户列出。
- 不同用户记忆完全隔离。
- 清空只影响当前用户。
- 最近 N 条只返回最新 N 条。
- 最近 N 条返回顺序为从旧到新。
- GetByID 正确用户可获取，错误用户返回 nil。
- DeleteByID 正确用户可删除，错误用户/不存在返回 false。
- UpdateContentByID 正确用户可更新，错误用户返回 nil。

### Service 测试

- `Remember` 成功保存 trim 后内容。
- 空内容返回 `ErrEmptyContent`。
- 超过 200 个 Unicode 字符返回 `ErrContentTooLong`。
- `List` 无记忆时返回空列表。
- `Clear` 在无记忆时仍成功。
- `GetRecentForContext` 返回字符串列表。
- `Delete` 正确用户可删除，不存在时返回 false。
- `Update` 正确用户可更新，trim 内容，空内容/超长拒绝。

### Message Service 测试

- 有长期记忆时注入 system message。
- 无长期记忆时不注入长期记忆 system message。
- 超过 10 条时只注入最近 10 条。
- 长期记忆查询失败时仍返回短期上下文和当前用户消息。
- 原有短期上下文最近 10 轮行为不变。

### WeChat Handler 测试

- `#记住 我偏好简洁回答` 返回保存成功和安全提醒。
- `#记住` 返回示例提示。
- `#记住    xxx` 保存 `xxx`。
- 超长内容返回长度限制。
- `#我的记忆` 有记忆时返回编号列表。
- `#我的记忆` 无记忆时返回空状态和示例。
- `#清空记忆` 返回成功。
- 普通对话仍调用 LLM，且 LLM messages 包含长期记忆。
- 记忆服务失败时返回统一服务不可用文案。
- `#删除记忆 2` 按序号删除并返回删除内容。
- `#删除记忆 2` 序号超出范围时返回提示。
- `#删除记忆 abc` 格式错误时返回示例。

## 验收映射

- `#记住` 保存成功：由 WeChat Handler、Memory Service、Repository 测试覆盖。
- `#我的记忆` 查看保存内容：由 WeChat Handler 和 Repository 测试覆盖。
- 超过 10 条只注入最近 10 条：由 Message Service 测试覆盖。
- `#清空记忆` 后显示空状态：由 WeChat Handler、Service、Repository 测试覆盖。
- 普通对话包含长期记忆：由 Message Service 和 WeChat Handler LLM messages 测试覆盖。
- 查询失败降级：由 Message Service mock 失败测试覆盖。
- 日志不输出完整记忆：由实现约束和代码审查覆盖。
- 用户隔离：由 Repository 测试覆盖。
- 短期上下文不受影响：由现有 Message Service 测试和新增回归测试覆盖。
- 用户自定义 LLM 配置不受影响：命令识别顺序和现有配置测试覆盖。

## 实施顺序

1. 新增 memory domain 和 AutoMigrate 注册。
2. 新增 memory repository。
3. 新增 memory service。
4. 扩展 message service 注入长期记忆。
5. 扩展 WeChat handler 命令处理。
6. 更新 routes 依赖装配。
7. 检查并修复完整消息内容日志。
8. 运行完整后端测试。
