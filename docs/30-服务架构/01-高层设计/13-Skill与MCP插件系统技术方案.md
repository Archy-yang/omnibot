# Skill 与 MCP 插件系统技术方案

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 已确认（2026-09-04） |
| PRD | [插件系统PRD-v1.0](../../20-产品PRD/in_progress/插件系统PRD-v1.0.md) |
| 上游规划 | [09-后续能力演进规划.md](./09-后续能力演进规划.md) 阶段 3 / 阶段 4 |
| 前置 | 08-后台Agent任务框架（能力白名单已落地）、11-Prompt管理 |

---

## 1. 背景与目标

工具目前全部硬编码：定义（名称/描述/参数 schema）与执行体耦合在 `builtin_tools.go` 的 Go 工厂函数里，装配点 `routes.go` 逐个 `Register`。扩展能力 = 改代码发版。

本方案把演进规划的阶段 3（skill 泛化）与阶段 4（MCP 接入）合并立项，分两个里程碑：

- **M1 skill 抽象**：工具定义数据化（落库、可启停），执行体留在代码注册表。
- **M2 MCP 客户端**：外部 MCP server 作为第二种 skill 来源，与内置 skill 统一调度。

已确认决策：配置系统级（配置文件）；MCP 走 HTTP Streamable（stdio 二期）；仅工具型 skill。

## 2. 现状与地基

| 已具备 | 位置 | 本方案的用法 |
|--------|------|--------------|
| `Tool` 统一接口 + `ToolRegistry` | `service/agent/tool.go` | 不动，skill 是它的"上游供货商" |
| 能力标签 + 白名单解析 | `tool_provider.go` | skill 携带 capabilities，原样下传 |
| 工具熔断/预算 hook | `tool_budget_hook.go` 等 | 按 tool name 生效，MCP 工具自动纳管 |
| PromptRegistry | `agentprompt/` | 不动（本期不做提示词型 skill） |
| AES 加密（用户 LLM key 先例） | `service/user` | M2 密钥处理沿用同一模式 |

## 3. 总体设计

```
                    ┌─────────────── SkillService（调度中枢）───────────────┐
                    │                                                      │
   定义来源 A        │  skills 表（定义+启停,单一事实源）      定义来源 B      │
   builtin 执行体注册表◄── 启动时 seed/upsert                  MCP client ───┤ (M2)
   (Go func,代码内)  │                                                      │  ListTools → upsert
                    ▼                                                      ▼
            BuildRegistries():  enabled ∧ executor 可用 的 skill → Tool
                    │
        ┌───────────┴───────────┐
        ▼                       ▼
  agentToolRegistry        globalToolRegistry
  (主 Agent,含框架工具)     (子 Agent 池,能力白名单裁剪)
```

核心原则：

1. **skill = 定义（数据）+ 执行体（代码/协议）**。定义统一落 `skills` 表；执行体两类——`builtin`（Go 闭包）与 `mcp`（远程调用）。
2. **框架工具不 skill 化**：`request_input`/`delegate`/`query_task`/`update_task` 是 Agent 的生存依赖（PRD 4.1"不可停用"），保持硬编码，不入 skills 表、不出现在清单里。
3. **执行体不可用 → 技能隐藏**：如 MCP server 断连，该 skill 不进 registry（而非进了但必失败），助手口径为"没有这个技能"。
4. **单一事实源**：运行时 registry 一律由 SkillService 构建，装配点不再逐个注册能力工具。

## 4. 数据模型

`internal/domain/skill/skill.go`：

```go
type Skill struct {
    ID          int64  `gorm:"primaryKey"`
    Name        string `gorm:"uniqueIndex;size:64"`   // 工具名(即 ToolRegistry key)
    DisplayName string `gorm:"size:64"`               // 面向用户的中文名
    Description string                                // 给 LLM 的描述
    Source      string `gorm:"size:16;index"`         // builtin / mcp
    ExecutorKey string `gorm:"size:64"`               // builtin:执行体注册表 key;mcp:server 内工具名
    Capabilities string `gorm:"size:128"`             // 逗号分隔,如 "research,web"
    ParamsSchema string `gorm:"type:text"`            // JSON Schema 字符串
    Enabled     bool
    MCPServerID *int64 `gorm:"index"`                 // source=mcp 时指向所属 server
    CreatedAt / UpdatedAt
}

// M2:
type MCPServer struct {
    ID       int64
    Name     string `gorm:"uniqueIndex;size:64"`
    BaseURL  string
    APIKey   string  // AES 加密存储(沿用 user_llm_configs 的加密模式)
    Enabled  bool
}
```

约束：

- `Name` 全局唯一（builtin 与 mcp 冲突时：MCP 工具重名 → 加载失败该条并在日志告警，不覆盖内置）。
- `ParamsSchema` 存 JSON 字符串，运行时 `json.Unmarshal` 为 `map[string]interface{}`；非法 schema 的 skill 视为执行体不可用（隐藏 + 告警）。
- seed 语义：启动时以代码内 builtin 定义 `upsert`（按 Name，更新描述/schema/capabilities，**不碰 Enabled**——用户启停状态优先于发版）。

## 5. M1：skill 抽象

### 5.1 执行体注册表（实现按 builder 模式落地）

> 实现细化（相对草案）：未新建独立的 ExecutorRegistry——**现有 `agent.CreateXXXTool()` 工厂本身就是
> builder**（`func() agent.Tool`，定义+执行体同源），`SkillService.RegisterBuiltin(builder)` 直接注册。
> 定义以代码为准（发版即更新，无漂移窗口），DB 定义列仅存档/展示。避免了两处定义的同步负担。

```go
type ToolBuilder func() agentpkg.Tool
// RegisterBuiltin(builder)          默认主 Agent 可见
// RegisterBuiltinSubOnly(builder)   子 Agent 专属(MainVisible=false,如 rss_reader/web_read)
```

另引入 `MainVisible` 标记（草案遗漏）：主 Agent 工具集刻意不含抓取类（方向 B：管家不亲自抓网页，
联网必须 delegate），skill 需要携带"是否进主 Agent 池"的信息，否则 skill 化会破坏该设计。

### 5.2 SkillService

`internal/service/skill/skill_service.go`：

```go
type SkillService struct {
    repo      SkillRepository
    executors *ExecutorRegistry
    mu        sync.RWMutex
}
func (s *SkillService) SeedBuiltins(defs []BuiltinDef) error          // 启动 upsert
func (s *SkillService) List() ([]SkillView, error)                    // 含 source/enabled
func (s *SkillService) SetEnabled(name string, enabled bool) error
func (s *SkillService) BuildRegistries() (main, global *agent.ToolRegistry, err error)
```

`ApplyTo`（实现命名）幂等重建规则：对技能名集合——先从两池移除，再把 `Enabled ∧ 执行体可用` 的加回
（`MainVisible=false` 的只进 global 池）；不碰注册在池里的框架工具（名字不属于技能集，天然不受影响）。
`ToolRegistry` 已加 RWMutex 并发安全（Agent 执行链读 registry 与启停重建并发）。
`SetEnabled` 落库后立即 ApplyTo 已绑定的 registry——停用即时生效，无需重启。

### 5.3 API

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/skills` | 清单：name/display_name/description/source/enabled（不含 schema 细节） |
| PUT | `/api/v1/skills/:name` | body `{enabled: bool}`，生效即时重建 registry |

### 5.4 前端

SettingsDrawer 新增「技能」tab：技能清单（名称/说明/来源徽标/开关）。来源徽标 builtin=「内置」；M2 起 mcp 显示所属服务名。

## 6. M2：MCP 客户端

### 6.1 配置（系统级）

```yaml
# config.yaml(不入 git,密钥明文仅存在于配置文件,与 llm.providers 现状一致)
mcp:
  servers:
    - name: "github"
      base_url: "https://mcp.example.com/mcp"
      api_key: "sk-xxx"        # 入库时 AES 加密,界面只回显掩码
      enabled: true
```

`pkg/config` 增加 `MCPConfig`/`MCPServerConfig`。增删改走配置文件 + 重启（第一版不做热加载，页面只读展示 + 启停已发现技能）。

### 6.2 客户端与接入流程

- 库：`github.com/mark3labs/mcp-go`（client，Streamable HTTP 传输）。
- `internal/service/skill/mcp_source.go`：
  - 启动时对每个 enabled server：`NewClient → Initialize → ListTools`，成功则把工具 upsert 进 `skills`（`source=mcp`，`executor_key=server 工具名`，`Enabled=false` 默认停用）；失败（超时/鉴权/不可达）则该 server 全部 skill 标记不可用，启动不阻塞。
  - 执行：`CallTool(ctx, name, args)`，超时 30s，错误按"技能暂时不可用"口径返回。
  - 连接策略：每次 Execute 惰性建立连接、失败重建（单用户低频，不维护长连接池）。
- 重启时对已消失的远端工具：保留行但标记不可用（executor 缺失自然隐藏），不删除——避免服务抖动导致启停状态丢失。

### 6.3 安全

- APIKey AES 加密落库（同 `user_llm_configs` 模式）；日志不输出 key 与完整工具参数（安全红线）。
- MCP 返回内容进入对话前不额外信任：与 web_read 同级处理（当前以文本注入上下文，不做指令隔离，作为已知限制记录）。
- 未 Enabled 的 mcp skill 不进 registry ⇒ 不会向外部服务发起任何请求（对齐 PRD 4.4）。

## 7. 测试计划（TDD）

### M1

| # | 测试 | 断言 |
|---|------|------|
| 1 | `TestSeedBuiltins_UpsertKeepsEnabled` | 二次 seed 更新描述但保留用户启停状态 |
| 2 | `TestBuildRegistries_SkipDisabled` | disabled 的 skill 不出现在两池 |
| 3 | `TestBuildRegistries_SkipMissingExecutor` | executor 缺失/schema 非法的 skill 隐藏 + 不报错 |
| 4 | `TestBuildRegistries_CapabilitiesRestore` | "research,web" 还原为 Tool.Capabilities |
| 5 | `TestSetEnabled_RebuildsRegistry` | 启停后 registry 立即增减该工具 |
| 6 | `TestSkillAPI_ListAndToggle` | GET/PUT 契约 + 非法 body 400 |
| 7 | 回归：框架工具（delegate/request_input 等）不 skill 化、行为不变 |

### M2

| # | 测试 | 断言 |
|---|------|------|
| 8 | `TestMCPSource_UpsertToolsDefaultDisabled` | ListTools 结果落库且默认停用 |
| 9 | `TestMCPSource_ConnectFailureNonBlocking` | server 不可达不阻塞启动、技能隐藏 |
| 10 | `TestMCPExecute_TimeoutAndError口径` | 超时返回"技能暂时不可用"类错误 |
| 11 | `TestMCPServerKey_EncryptedAtRest` | 落库密文、回显掩码 |
| 12 | 名称冲突：mcp 工具与内置重名 → 跳过 + 告警 |

## 8. 边界与不做

- 用户自定义执行体（脚本/代码）：安全红线，不做。
- stdio 传输、MCP 热加载、resources/prompts 等 MCP 高级特性：二期。
- 提示词型 skill：另一条线（PromptRegistry），本期不混入。

## 9. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| M1 | skill 抽象 + 内置工具迁移 + 清单/启停 API + 前端技能 tab | 本方案 §5、PRD 4.1/4.3 |
| M2 | MCP 客户端 + 配置 + 技能来源 mcp | 本方案 §6、PRD 4.2/4.4 |

---

**文档版本**：v1.0
**创建日期**：2026-09-04
