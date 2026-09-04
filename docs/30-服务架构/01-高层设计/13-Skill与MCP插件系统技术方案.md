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

### 6.1 配置（M3 起在线配置，已落地）

**M3 修订**：MCP server 配置从 config.yaml 迁移到 **数据库（`mcp_servers` 表）**，Web 端「技能」抽屉在线增删改查——兑现 PRD 4.2 的完整形态。config.yaml 的 `mcp.servers` 段降级为**首次启动 seed**（库空且有配置时导入一次，加密落库，此后 DB 为唯一事实源）。

- APIKey **AES 加密落库**（`crypto.Encrypt`，密文带 `enc:` 前缀），接口只回显 `has_api_key` 布尔；更新时空 key = 保留原值。
- 增/改/删 server **立即同步**（连接 → ListTools → 落 skills 表/清技能行），无需重启；同步失败以 `SyncResult.Err` 可读返回，不阻断保存。
- 停用 server（enabled=false）= 不连接 + 执行体移除（技能隐藏）；删除 server 级联删其技能行。
- API：`GET/POST /api/v1/mcp/servers`、`PUT/DELETE /api/v1/mcp/servers/:id`、`POST /api/v1/mcp/servers/:id/sync`。

```yaml
# config.yaml —— 仅首次启动 seed(库内已有配置时本段被忽略)
mcp:
  servers:
    - name: "github"
      base_url: "https://mcp.example.com/mcp"
      api_key: "sk-xxx"
      enabled: true
```

### 6.4 OAuth 2.1 支持（M4，已落地）

远程托管 MCP server 的标准鉴权（MCP 2025-03-26 规范引入）。基于 `mcp-go` OAuthHandler：
授权码 + PKCE，支持**授权服务器元数据发现**（`/.well-known/oauth-authorization-server`）、
**动态客户端注册**（RFC 7591，Client ID 留空时自动注册）、**refresh token 自动刷新**。

- `mcp_servers` 表新增列：`auth_type`（none/bearer/oauth）、`oauth_client_id`、
  `oauth_client_secret`（加密）、`oauth_scopes`、`oauth_tokens`（Token JSON 整体加密）。
- Token 持久化：`dbTokenStore`（实现 mcp-go `TokenStore`）——授权换新与刷新自动落库，重启不丢。
- 流程：`POST /api/v1/mcp/servers/:id/authorize`（挂起 state+verifier，返回授权 URL）
  → 用户在服务商页授权 → 重定向 `GET /api/v1/mcp/oauth/callback`（不挂 JWT，一次性 state 防 CSRF）
  → 换 token 加密落库 → 「同步」发现工具。
- redirect_uri = `<app.external_url>/api/v1/mcp/oauth/callback`（配置 `app.external_url`，
  空回落 `http://localhost:<port>`；自部署在公网需设置该项）。
- 未授权的 oauth server 同步被拒（"尚未完成 OAuth 授权"）；token 过期连接前自动刷新，失败如实上报。

### 6.2 客户端与接入流程（实现按 mcp-go v0.32.0 落地）

- 库：`github.com/mark3labs/mcp-go`（client，Streamable HTTP 传输；go 1.24 兼容上限 v0.32.0）。
- `internal/service/skill/mcp_source.go`：
  - `MCPClient` 窄接口（Start/Initialize/ListTools/CallTool）+ `MCPClientFactory`，测试注入 mock；
    真实实现 `NewStreamableHTTPMCPClient`（APIKey 走 `Authorization: Bearer` 头）。
  - 启动时对每个 enabled server：`Start → Initialize → ListTools`，成功则把工具 upsert 进 `skills`
    （`source=mcp`，`Enabled=false` 默认停用，重复同步保留用户启停）；失败（超时/鉴权/不可达）
    则该 server 技能隐藏，**启动不阻塞**。
  - 与内置技能重名的远端工具跳过 + 日志告警（不覆盖内置）。
  - 从配置移除的 server：`DeleteMCPSkillsNotIn` 清理其技能行。
  - 执行：同步成功即注册执行闭包（`CallTool` + 30s 超时 + 文本内容抽取）；`IsError` 的远端结果
    以错误返回（助手如实告知用户）；server 下线（执行体缺失）技能隐藏。
- 远端工具默认 `MainVisible=true`（抓取类限制只针对内置工具）。

### 6.3 安全

- 密钥仅存于 config.yaml（不入 git、不入库）；日志不输出 key 与完整工具参数（安全红线）。
- MCP 返回内容进入对话前不额外信任：与 web_read 同级处理（当前以文本注入上下文，不做指令隔离，作为已知限制记录）。
- 未 Enabled 的 mcp skill 不进 registry，且 server 未开启（`enabled: false`）时**不发起任何连接**（对齐 PRD 4.4）。

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
