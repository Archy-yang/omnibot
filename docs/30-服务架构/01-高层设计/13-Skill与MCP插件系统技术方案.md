# Tool 与 MCP 插件系统技术方案

> **正名修订（2026-09-25）**：概念三分——**Tool（工具）**= function-call 可执行单元，
> 定义落 `tools` 表（原 `skills` 表已迁移,`domain/skill`→`domain/tool`、`service/skill`→
> `service/tool`+`service/mcp`、API `/api/v1/skills`→`/api/v1/tools`）；**MCP 连接器**= 外部
> server,其工具目录只在内存缓存不入库（B2 按需加载）；**Skill（技能）**概念留白，未来 =
> prompt+工具组合的能力包。本文其余章节中的 "skill" 均按此对照读作 "tool"（或连接器）。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.2 |
| 状态 | 已确认（2026-09-04;2026-09-25 正名修订;2026-09-25 MCP 客户端迁移官方 go-sdk） |
| PRD | [插件系统PRD-v1.0](../../20-产品PRD/completed/插件系统PRD-v1.0.md) |
| 上游规划 | [15-后续能力演进规划(已归档)](../02-历史归档/15-后续能力演进规划.md) 阶段 3 / 阶段 4 |
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
| `Tool` 统一接口 + `ToolRegistry` | `internal/pkg/toolcore`（43a96e9 下沉的中立包，agent/tool/mcp 共同依赖） | 不动，ToolService 是它的"上游供货商" |
| 能力标签 + 白名单解析 | `service/agent/tool_provider.go` | 工具携带 capabilities，原样下传 |
| 工具熔断/预算 hook | `tool_budget_hook.go` 等 | 按 tool name 生效，MCP 工具自动纳管 |
| PromptRegistry | `internal/agentprompt/` | 不动（本期不做提示词型 skill） |
| AES 加密（用户 LLM key 先例） | `service/user` | MCP 密钥处理沿用同一模式 |

## 3. 总体设计（2026-09 正名后现行版）

```
                    ┌─────────────── ToolService（调度中枢）────────────────┐
                    │                                                       │
   定义来源 A        │  tools 表（builtin 定义+启停,单一事实源）  来源 B       │
   builtin 执行体注册表◄── 启动时 SeedBuiltins upsert        MCP 连接器 ─────┤
   (Go 工厂,代码内)  │   (MCP 工具目录内存化,不入 tools 表)     (service/mcp) │  ListTools →
                    ▼                                        MCPToolCatalog ▼ (进程内缓存)
            ApplyTo():  enabled ∧ executor 可用 的 tool → Tool(toolcore.Tool)
                    │
        ┌───────────┴───────────┐
        ▼                       ▼
  agentToolRegistry        globalToolRegistry
  (主 Agent,含框架工具)     (子 Agent 池,能力白名单裁剪)
```

核心原则（现行实现口径）：

1. **tool = 定义（数据）+ 执行体（代码/协议）**。**内置工具**定义统一落 `tools` 表（发版即
   seed 更新）；**MCP 工具目录内存化**（`mcp_catalog.go` 的 `MCPToolCatalog`，按 server 整目录
   同步重建+向量化，不入库）——两者最终都汇聚为 `toolcore.Tool` 进运行时 registry。
2. **框架工具不 tool 化**：`request_input`/`delegate`/`query_task`/`update_task` 是 Agent 的生存依赖
   （PRD 4.1"不可停用"），保持硬编码，不入 tools 表、不出现在清单里。
3. **执行体不可用 → 工具隐藏**：`ToolView.Available=false` 时不进运行时 registry（而非进了但必
   失败），助手口径为"没有这个工具"。
4. **单一事实源**：运行时 registry 一律由 ToolService 构建与重建（`ApplyTo`），装配点不逐个注册
   能力工具。
5. **MCP 调用收敛为单工具**：主 Agent 侧暴露 `mcp_call`(B2 按需加载——每轮按问题语义匹配相关
   MCP 工具注入上下文)，而非把每个 MCP 工具平铺成独立 function。

## 4. 数据模型

`internal/domain/tool/tool.go`（原 `domain/skill/skill.go`，2026-09-25 正名迁移）：

```go
type Tool struct {
    ID           int64  `gorm:"primaryKey;autoIncrement"`
    Name         string `gorm:"uniqueIndex;size:64;not null"` // 工具名(ToolRegistry key)
    DisplayName  string `gorm:"size:64"`                      // 面向用户的中文名
    Description  string `gorm:"type:text"`                    // 给 LLM 的描述
    Capabilities string `gorm:"size:128"`                     // 逗号分隔,如 "research,web"
    ParamsSchema string `gorm:"type:text"`                    // JSON Schema 字符串
    Enabled      bool   `gorm:"not null"`                     // 用户启停(builtin seed 不覆盖)
    MainVisible  bool   `gorm:"not null"`                     // 是否进主 Agent 池(false=子 Agent 专属)
    CreatedAt / UpdatedAt
}
```

MCP server 配置见 §6（M3 起在线配置，`domain/mcp.MCPServer` 落库，密钥 AES 加密）；
MCP **工具**不落库（内存目录，随连接同步）。

约束：

- `Name` 全局唯一（builtin 与 MCP 冲突时：MCP 工具重名 → 加载失败该条并在日志告警，不覆盖内置）。
- `ParamsSchema` 存 JSON 字符串，运行时 `json.Unmarshal`；非法 schema 视为执行体不可用（隐藏 + 告警）。
- seed 语义：启动时以代码内 builtin 定义 `upsert`（`SeedBuiltins`，按 Name 更新描述/schema/
  capabilities，**不碰 Enabled**——用户启停状态优先于发版）。

## 5. M1：tool 抽象（原"skill 抽象"，正名）

### 5.1 执行体注册表（实现按 builder 模式落地）

> 实现细化（相对草案）：未新建独立的 ExecutorRegistry——**现有 `agent.CreateXXXTool()` 工厂本身就是
> builder**（`func() agent.Tool`，定义+执行体同源），`ToolService.RegisterBuiltin(builder)` 直接注册。
> 定义以代码为准（发版即更新，无漂移窗口），DB 定义列仅存档/展示。避免了两处定义的同步负担。

```go
type ToolBuilder func() agentpkg.Tool
// RegisterBuiltin(builder)          默认主 Agent 可见
// RegisterBuiltinSubOnly(builder)   子 Agent 专属(MainVisible=false,如 rss_reader/web_read)
```

另引入 `MainVisible` 标记（草案遗漏）：主 Agent 工具集刻意不含抓取类（方向 B：管家不亲自抓网页，
联网必须 delegate），skill 需要携带"是否进主 Agent 池"的信息，否则 skill 化会破坏该设计。

### 5.2 ToolService（原 SkillService，正名迁移）

`internal/service/tool/tool_service.go`：

```go
type ToolService struct {
    repo       ToolRepository   // 窄接口,UpsertBuiltin/List/SetEnabled...
    builders   map[string]ToolBuilder
    main, global *toolcore.ToolRegistry // BindRegistries 绑定
    mu           sync.RWMutex
}
func (s *ToolService) RegisterBuiltin(builder ToolBuilder)          // 默认主 Agent 可见
func (s *ToolService) RegisterBuiltinSubOnly(builder ToolBuilder)   // 子 Agent 专属
func (s *ToolService) BindRegistries(main, global *toolcore.ToolRegistry) error
func (s *ToolService) SeedBuiltins() error                          // 启动 upsert(按注册表 seed)
func (s *ToolService) List() ([]ToolView, error)                    // name/description/enabled/available
func (s *ToolService) SetEnabled(name string, enabled bool) error
func (s *ToolService) ApplyTo(main, global *toolcore.ToolRegistry) error // 幂等重建
```

`ApplyTo` 幂等重建规则：对工具名集合——先从两池移除，再把 `Enabled ∧ 执行体可用` 的加回
（`MainVisible=false` 的只进 global 池）；不碰注册在池里的框架工具（名字不属于工具集，天然不受影响）。
`ToolRegistry` 已加 RWMutex 并发安全（Agent 执行链读 registry 与启停重建并发）。
`SetEnabled` 落库后立即 ApplyTo 已绑定的 registry——停用即时生效，无需重启。

### 5.3 API（原 `/api/v1/skills`，正名迁移）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/tools` | 清单：name/display_name/description/enabled/available（不含 schema 细节） |
| PUT | `/api/v1/tools/:name` | body `{enabled: bool}`，生效即时重建 registry |

### 5.4 前端

SettingsDrawer「工具」区：工具清单（名称/说明/来源徽标/开关）。来源徽标 builtin=「内置」；MCP 工具经 `mcp_call` 调度，不逐个出现在清单。

## 6. M2：MCP 客户端

### 6.1 配置（M3 起在线配置，已落地）

**M3 修订**：MCP server 配置从 config.yaml 迁移到 **数据库（`mcp_servers` 表）**，Web 端「技能」抽屉在线增删改查——兑现 PRD 4.2 的完整形态。config.yaml 的 `mcp.servers` 段降级为**首次启动 seed**（库空且有配置时导入一次，加密落库，此后 DB 为唯一事实源）。

- APIKey **AES 加密落库**（`crypto.Encrypt`，密文带 `enc:` 前缀），接口只回显 `has_api_key` 布尔；更新时空 key = 保留原值。
- 增/改/删 server **立即同步**（连接 → ListTools → 重建内存工具目录），无需重启；同步失败以 `SyncResult.Err` 可读返回，不阻断保存。
- 停用 server（enabled=false）= 不连接 + 执行体移除（工具隐藏）；删除 server 级联清其目录。
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

### 6.4 OAuth 2.1 支持（M4，已落地；2026-09-25 随 go-sdk 迁移改为自有实现）

远程托管 MCP server 的标准鉴权（MCP 2025-03-26 规范引入）。原基于 mcp-go OAuthHandler，
2026-09-25 客户端迁移官方 go-sdk 后改为**自有实现**（`mcp_oauth.go`）：
授权码 + PKCE（自实现 `crypto/rand`+SHA-256 S256），**授权服务器元数据发现**（RFC 8414，
`/.well-known/oauth-authorization-server`）、**动态客户端注册**（go-sdk `oauthex.RegisterClient`，
Client ID 留空时自动注册）、**refresh token 自动刷新**（标准 OAuth2 POST 自实现）。

- `mcp_servers` 表新增列：`auth_type`（none/bearer/oauth/query）、`oauth_client_id`、
  `oauth_client_secret`（加密）、`oauth_scopes`、`oauth_tokens`（Token JSON 整体加密）。
- Token 持久化：`dbTokenStore`（自有 `oauthTokenStore` 窄接口）——授权换新与刷新自动落库，重启不丢。
  Token JSON 字段与原 mcp-go `clienttransport.Token` 完全一致，**存量加密 token 直接可解析，
  已授权连接器无需重新授权**。
- 连接时 OAuth 客户端 == Bearer 客户端：service 层连接前刷新 token 后以 access token 走
  `Authorization: Bearer` 注入（`headerRoundTripper`），传输层不感知鉴权类型。
- 流程：`POST /api/v1/mcp/servers/:id/authorize`（挂起 state+verifier，返回授权 URL）
  → 用户在服务商页授权 → 重定向 `GET /api/v1/mcp/oauth/callback`（不挂 JWT，一次性 state 防 CSRF）
  → 换 token 加密落库 → 「同步」发现工具。
- redirect_uri = `<app.external_url>/api/v1/mcp/oauth/callback`（配置 `app.external_url`，
  空回落 `http://localhost:<port>`；自部署在公网需设置该项）。
- 未授权的 oauth server 同步被拒（"尚未完成 OAuth 授权"）；token 过期连接前自动刷新，失败如实上报。

### 6.2 客户端与接入流程（2026-09-25 迁移官方 go-sdk 落地）

- 库：`github.com/modelcontextprotocol/go-sdk` v1.7.0（**官方 SDK**，取代 mark3labs/mcp-go）。
  支持协议版本 2024-11-05 ~ 2026-07-28；传输 `StreamableClientTransport`（现行）+
  `SSEClientTransport`（2024-11 旧协议,高德等端点）。go 1.25+（本机已升 go 1.27.1）。
- `internal/service/mcp/mcp_client_gosdk.go`：
  - `MCPClient` 窄接口（ListTools/CallTool/Close,参数与返回为 `domain/mcp` 自有 DTO,
    SDK 类型不出实现文件）+ `MCPClientFactory`,测试注入 mock;真实实现
    `NewStreamableHTTPMCPClient`（query=key URL 参数 / Bearer 头两种鉴权,
    Bearer 经 `headerRoundTripper` 注入）。
  - 会话由 SDK `Connect` 一步建立（协议握手内聚）,原 mcp-go 的 `Start → Initialize`
    两步取消;客户端用完即 `Close()`。
  - streamable 传输 `http.Client.Timeout=0`（standalone SSE 常驻流不能一刀切超时）,
    超时纪律由调用方 ctx 负责（调用 30s / 同步 30s）;SSE 传输保留 30s Timeout（同 mcp-go 行为）。
  - 空传输回退链（主选失败 → SSE / query 组合逐试,生效组合持久化）逻辑不变。
- 集成测试：用 go-sdk **server 侧**（`NewServer`+`StreamableHTTPHandler`/`SSEHandler`）起
  真协议假 MCP server,覆盖 streamable/SSE/query/超时/远端报错全链路（`mcp_client_gosdk_test.go`）;
  live smoke 受 `LIVE_SMOKE=1` 门控直连真库真站（`mcp_live_smoke_test.go`,高德 15 工具实测通过）。

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

## 7.5 M5：飞书 CLI 桥接（2026-09-09 落地）

> 选型验证结论（三轮实测）：飞书官方托管 MCP（mcp.feishu.cn）不支持多维表格且需自定义头 + TAT 刷新；
> 本地 lark-mcp 覆盖全但文档编辑弱于托管版；官方 **lark-cli**（`@larksuite/cli`，2500+ API）
> 以用户身份全覆盖（文档 block 级编辑 + 多维表格 + 日历/邮件等），device flow 一次授权、
> token 入系统钥匙串自动刷新——个人助手场景的正解，M5 采纳。lark-mcp/stdio 传输放弃，
> 托管端点（自定义头 + TAT）留作"应用身份/多用户"场景的后续备选。

新增 builtin skill `feishu`：受控执行 lark-cli（`internal/service/agent/feishu_tool.go`）。

- **执行模型**：单工具覆盖全业务域。参数 `args: string[]` 逐字传给 CLI（`exec.CommandContext`
  参数数组直传，不经 shell，防注入）；多行内容走参数 `-` + `stdin` 透传，免转义。
- **安全收口**：域白名单（docs/base/drive/im/wiki/sheets/calendar/task/mail/… 及 schema/skills/api；
  排除 auth/config——凭证与授权、event/apps——事件与部署）；拒绝 `--yes`（高危写不经对话）；
  超时默认 60s；输出截断 32KB（错误场景保留尾部——CLI 的 JSON 错误含修复建议，对 LLM 有价值）。
- **可用性**：运行时检查——CLI 缺失/未授权时执行返回引导文案（安装 + `lark-cli auth login`），
  用户授权后自愈，无需重启；技能默认启用（`MainVisible=true`，主/子 Agent 均可用）。
- **身份**：固定 user（本期单用户）；CLI 的 token 管理完全自管，本工具零凭证管理。
- **配置**：`feishu.cli.bin_path`（默认 `lark-cli`）、`feishu.cli.timeout_seconds`（默认 60）。
- 测试：`feishu_tool_test.go` 9 项（定义/参数透传/stdin/`--yes` 拒绝/白名单/超时/截断/缺失引导/空参）。

## 8. 边界与不做

- 用户自定义执行体（脚本/代码）：安全红线，不做。
- stdio 传输、MCP 热加载、resources/prompts 等 MCP 高级特性：放弃（被 lark-cli 覆盖，见 §7.5）；
  MCP 托管端点自定义头 + TAT（飞书）留作后续备选。
- lark-cli 高危写（`--yes`）：本期不对 Agent 开放。
- 提示词型 skill：另一条线（PromptRegistry），本期不混入。

## 9. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| M1 | skill 抽象 + 内置工具迁移 + 清单/启停 API + 前端技能 tab | 本方案 §5、PRD 4.1/4.3 |
| M2 | MCP 客户端 + 配置 + 技能来源 mcp | 本方案 §6、PRD 4.2/4.4 |
| M5 | 飞书 CLI 桥接（用户身份全能通道） | 本方案 §7.5 |

---

**文档版本**：v1.1（M5）
**创建日期**：2026-09-04
