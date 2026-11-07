# 32 - 联网搜索 web_search 技术方案

> 2026-11-02 立项。目标：在**不改动现有抓取能力**的前提下，新增一个可配置三方服务的
> `web_search` 工具，把「找线索」与「读全文」拆成两步。
> 规划来源：用户提议（2026-11-02）——"现有的抓取网页能力不变，再增加一个复杂查询的能力，
> 使用三方 search api，例如 DeepSeek 的 search api，配置服务地址、api key 与是否启用"。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.1（已落地） |
| 状态 | **已落地**（2026-11-03，v2.10.0）——决策与偏差见 §12 |
| 上游 | [product-vision v2.0](../../10-宪章/product-vision-v2.0.md)、[13-Skill与MCP插件系统技术方案](./13-Skill与MCP插件系统技术方案.md)、[16-架构迭代路线图](./16-架构迭代路线图-V1-对话与上下文演进.md) |
| 目标版本 | v2.10.0 |
| 影响面 | 新增 1 张表、1 个工具、1 组配置 API、1 张前端配置卡；**web_read 零改动** |

---

## 1. 结论先行

三句话：

1. **`web_read` 不动。** 它解决的是"已知 URL，读正文"，继续走本地 readability + Jina 升级。
2. **新增 `web_search`。** 解决"只有问题，先找有哪些页面"——一次 HTTP 调用拿回标题/URL/摘要，
   不下载页面，成本与延迟都远低于抓取。
3. **配置按用户存库、密钥加密。** 与 `user_llm_configs` 同一范式：地址、Key、条数、开关，
   每用户独立，互不影响。

分工写进工具描述，由模型自己选：**search 找线索 → read 读正文**，而不是让 search 兼职抓取。

---

## 2. 现状盘点（方案的前提）

| 现有能力 | 实现 | 边界 |
|----------|------|------|
| `web_read` | `internal/service/agent/tools/web_read.go`，mode=auto/http/reader；本地 readability+goquery，正文不足自动升级 Jina Reader | **子 Agent 专属**（`RegisterBuiltinSubOnly`）——主 Agent 是管家，联网抓取必须派活 |
| `rss_reader` | 同上，订阅源优先 | 子 Agent 专属 |
| `weather` | NMC 中央气象台，零 key | 主 Agent 直调（单点结构化查询，不算"抓网页"） |
| 工具注册 | `toolSvc.RegisterBuiltin*` → 定义 upsert 进 `tools` 表 → 运行时 registry 按用户启停过滤 | 工具开关是**系统级**（`tools` 表无 user_id） |
| 用户级密钥 | `user_llm_configs.api_key`、MCP server api_key：`crypto.Encrypt` 落库，接口只回显掩码 | 已有成熟范式可抄 |

**ctx 用户身份已全链路可用**：`service.RunStream` 对主 Agent 注入 `withUserID`，
子 Agent 在 `sub_agent_runner.go` 同样注入（`svc.RunStream(withTaskID(ctx, taskID), userID, ...)`）。
所以工具内部用 `toolcore.UserIDFromContext(ctx)` 取用户配置是可行的，主/子 Agent 都通。

---

## 3. 三方 API 契约（已核实，非猜测）

来源：[DeepSeek Search & Fetch 官方文档](https://api-docs.deepseek.com/guides/search)。

```
POST {base_url}/v1/tools/search
Authorization: Bearer <API Key>
Content-Type: application/json

{"query": "...", "count": 5, "need_content": false, "ori_question": "..."}
```

| 请求字段 | 说明 |
|----------|------|
| `query` | 必填，1~128 字符，支持 `site:` / `-site:` / `lang:` / `after:` / `before:` / `"精确短语"` 等操作符 |
| `count` | 1~50，默认 10 |
| `need_content` | 是否返回 `content` 正文，默认 false |
| `ori_question` | 触发本次搜索的用户原问题，用于提升语义匹配 |

响应：

```json
{"results": [{"rerank_score": 2.4, "title": "...", "url": "https://...",
  "snippet": "...", "summary": "...", "content": "", "site_name": "...",
  "date": "2026-06-01T00:00:00+00:00", "authority": 3}]}
```

错误遵循 OpenAI 兼容格式；计费按**成功请求**计（空结果不计费），QPS 100。

> **可扩展性说明**：`provider` 字段留在表里（v1 只有 `deepseek`）。若要接博查 / Tavily / Exa，
> 只新增一个 adapter 实现，不动表结构与 API。

---

## 4. 方案设计

### 4.1 分层与新增文件

```
internal/domain/search/config.go          # 配置实体 + 字段校验(provider 白名单/count 范围/URL scheme)
internal/repository/search/config_repo.go # GORM 实现(窄接口在 service 侧声明)
internal/service/search/service.go        # 配置 CRUD(加密/掩码/留空保持) + Search 编排 + 结果格式化
internal/service/search/deepseek.go       # HTTP adapter:请求 DTO/响应 DTO/状态码→中文可行动文案
internal/service/agent/tools/web_search.go# toolcore.Tool 薄壳:取 ctx userID → 调 service
internal/api/web/search_config_handler.go # GET/PUT/DELETE /api/v1/user/search-config
```

依赖方向（守住宪章分层）：

```
api → service/search ──→ repository/search ──→ domain/search
              ↑
service/agent/tools(只依赖 service/search 声明的窄接口)
```

`tools` 包**不 import** repository / gorm；它只认 `Searcher` 窄接口（`Search(ctx, userID, query, count)`）。

### 4.2 数据模型：`user_search_configs`

| 列 | 类型 | 说明 |
|----|------|------|
| `id` | bigint PK | |
| `user_id` | bigint uniqueIndex | 一用户一条 |
| `provider` | varchar(32) | v1 固定 `deepseek`；白名单校验 |
| `base_url` | varchar(256) | 默认 `https://api.deepseek.com` |
| `api_key` | varchar(512) | **密文**（`crypto.Encrypt`） |
| `enabled` | bool default true | 用户开关（关闭=助手不再使用搜索） |
| `default_count` | int default 5 | 1~20（低于 API 上限 50，控 token） |
| `need_content` | bool default false | 默认不取正文，交给 `web_read` |
| `timeout_seconds` | int default 20 | 5~60 |
| `created_at` / `updated_at` | timestamp | |

**为什么按用户而不是写 config.yaml**：多用户各自持 Key、各自计费；且与 LLM 配置的既有交互一致
（设置抽屉里配，不用改配置文件重启）。

### 4.3 配置服务语义（照抄 LLM 配置的成熟做法）

| 场景 | 行为 |
|------|------|
| 保存时 Key 留空 | **保持原密钥**，不覆盖（前端不回显明文，只回显 `sk-****abcd`） |
| 保存时传 `clear_key` | 清除密钥（配合停用） |
| 未配置 / `enabled=false` | 工具仍存在，但 Execute 返回「联网搜索未配置」的可行动提示，模型据此改用 `web_read` |
| Key 加解密失败 | 视为未配置（不 panic、不泄露密文），错误日志只记 user_id 与错误类型 |

校验（domain 层，返回中文错误）：

- `base_url` 必须 `http/https` 且有主机名；**允许内网/本机地址**（这是用户自己填的服务地址，
  属于用户信任域，与 `web_read` 的 SSRF 防护场景不同——后者 URL 来自 LLM，可能被诱导）。
  这一点必须在代码注释里写明，避免后续安全审查误加校验。
- `default_count` ∈ [1,20]；`timeout_seconds` ∈ [5,60]；`provider` ∈ {deepseek}。

### 4.4 工具契约

```go
toolcore.Tool{
  Name:         "web_search",
  DisplayLabel: "检索了网络",
  Capabilities: []string{toolcore.CapResearch, toolcore.CapWeb},
  Parameters:   {query: string(必填), count: int(选填 1-20), need_content: bool(选填)},
}
```

**Description（决定模型会不会用对，需精雕）**：

> 用搜索引擎检索网页，返回标题、链接与摘要。适合「有哪些相关页面 / 最近发生了什么」这类
> 还不确定该读哪个页面的时候；需要读某个页面的正文时用 web_read，不要用本工具反复换措辞重试。
> query 支持 `site:example.com`、`after:2026-01-01`、`lang:zh`、`"精确短语"`、`-排除词` 等操作符。

**可见性（决策点，见 §10-1）**：建议**主 Agent 可见**。理由是与 `weather` 同类——
单点结构化查询，一次调用即答，不属于"抓取网页"那条边界（那条针对的是下载/渲染/解析）；
若做成子 Agent 专属，"今天有个什么新闻"也要派活等 30 秒，体验反而倒退。

**输出格式**（结构化纯文本，非 JSON——模型对纯文本更稳，且好截断）：

```
搜索「<query>」· 5 条

1. <title>
   <url>
   摘要：<summary 截断 400 字>
   来源：<site_name> · <date 的 YYYY-MM-DD>
2. ...

（需要读全文时，用 web_read 打开上面的链接）
```

预算控制：`count` 默认 5、摘要每条截 400 字 → 单次约 1000 token；
`need_content=true` 时每条 `content` 截 2000 字节且总预算 8000 字节
（用既有的 `textutil.TruncateUTF8Safe`，避免非法 UTF-8 被 PG 拒收）。

空结果、未配置、失败，都返回**可行动的中文文本**而不是 error（error 会触发熔断计数，
而"没搜到"是正常业务结果，不该计入失败）：

| 情况 | 返回文本 |
|------|----------|
| 空结果 | 没有搜到与「x」相关的结果。可以换关键词，或直接用 web_read 读已知页面。 |
| 未配置/停用 | 联网搜索还没配置，暂时用不了。改用 web_read，或如实告诉用户没查到。 |
| 401 | 搜索服务的密钥无效，需要用户在设置里重新填写。 |
| 402 | 搜索服务账户余额不足。 |
| 429 | 搜索服务限流，稍后再试，或先用 web_read。 |
| 超时/网络 | 搜索超时/不可达，稍后再试。 |
| 5xx | 搜索服务暂时不可用。 |

> 失败文案统一走 `sanitize` 白名单映射，**不透传三方原始响应体**（可能含内部字段）。

### 4.5 API 契约

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/user/search-config` | 回显：`has_config` / `enabled` / `provider` / `base_url` / `api_key_masked` / `default_count` / `need_content` / `timeout_seconds` |
| PUT | `/api/v1/user/search-config` | 保存（Key 留空=保持；`clear_key=true`=清除） |
| DELETE | `/api/v1/user/search-config` | 删除整条配置（回到未配置态） |

全部挂 `middleware.AuthRequired`，用户 id 从 JWT 取，**不接受 body 里的 user_id**（越权红线）。

### 4.6 前端（ToolDrawer「工具」tab）

工具列表上方加一张「联网搜索」卡片：

```
联网搜索                              [开关]
用三方搜索服务找线索；读全文仍交给「读取网页」。
服务地址  [https://api.deepseek.com        ]
API Key   [••••••••••••  (留空表示不修改)  ]
每次条数  [5]   超时(秒) [20]   [ ] 同时取回正文
                                  [保存]
```

- 开关独立于工具列表里的开关：卡片开关 = 这个用户有没有可用后端；列表开关 = 全局是否让模型看到
  `web_search`。卡片里写一句提示避免误解（见 §10-2）。
- `api_key_masked` 回显掩码，输入框 placeholder 提示"留空表示不修改"。
- 保存后 toast；未配置时工具列表照样显示该工具（可用性由执行期判定）。

### 4.7 提示词同步（不改会被模型用错）

- **子 Agent 信息源规则**（`agentprompt/content.go` `SubSourceRulesPrompt`）：
  现有顺序是 `manage_subscriptions(list) → rss_reader → 常规检索`。
  在"常规检索"处点名：先 `web_search` 找线索，再 `web_read` 读 2~3 个最相关页面，
  不要对同一问题反复改词重搜。
- **主 Agent**：不新增段落。`web_search` 的 Description 已足够；`MainResponseStylePrompt`
  的"不提工具名"约束照旧生效（说"我查了一下"即可）。

---

## 5. 安全红线对照

| 红线 | 本方案的落实 |
|------|--------------|
| 用户敏感数据加密存储 | `api_key` 走 `crypto.Encrypt`；接口只回显掩码；DB 里无明文 |
| 日志不输出敏感信息 | 只记 `user_id` / `provider` / `result_count` / `latency_ms` / `status_code`；**不记 query 全文、不记 Key、不记响应体** |
| 用户输入校验 | query 长度 1~128、count 范围、base_url scheme、provider 白名单，全部在 domain 层拦 |
| 错误不泄露内部细节 | 状态码 → 固定中文文案（§4.4 表），原始 body 只在 debug 日志截断到 200 字且脱敏 |
| 越权 | 配置读写一律以 JWT user_id 为准 |

---

## 6. 兼容性与影响面

- **DB**：新增 `user_search_configs`（AutoMigrate 自动建，零回填）。
- **工具清单**：`tools` 表新增一行 `web_search`（`SeedBuiltins` 幂等 upsert，默认启用）；
  存量用户的工具列表会多出这一项——属于功能新增，无破坏性。
- **`web_read` / `rss_reader` / 天气**：零改动。
- **前端**：新增一张卡片与一组 API 调用；无既有组件行为变更。
- **版本**：`APP_VERSION` → v2.10.0；CHANGELOG 记录。

---

## 7. TDD 实施顺序（每步先写失败测试）

| # | 步骤 | 测试要点 |
|---|------|----------|
| N1 | domain 校验 | 非法 scheme / 空 host / count 越界 / 未知 provider 全部报错；默认值填充 |
| N2 | repo | 落库是密文（`crypto.Decrypt` 能还原、原始明文不出现）；按 user_id 唯一；不存在返回 nil |
| N3 | service 配置 CRUD | Key 留空保持原值；`clear_key` 清除；掩码格式 `sk-****abcd`；解密失败视为未配置 |
| N4 | deepseek adapter | httptest 覆盖：正常 / 空 results / 401 / 402 / 429 / 500 / 超时 / 畸形 JSON / 缺字段 |
| N5 | 结果格式化 | 截断遵守字节预算且落在 UTF-8 边界；空结果返回"没搜到"文案；日期格式化容错 |
| N6 | tool | 未配置→可行动文案且**不**返回 error；正常→含 title/url/summary；ctx 无 userID→未配置；参数非法→error |
| N7 | handler | 未登录 401；GET 不回显明文；PUT 留空不改 key；DELETE 后 GET 回 `has_config=false` |
| N8 | 前端 | 手测：配置→保存→重开抽屉回显掩码→工具列表开关生效 |

每步 `go test ./...` 全绿再进下一步；N4/N6 用 `httptest`，不依赖真实网络（避免 CI 抖动与扣费）。

---

## 8. 不做（明确划界）

- **不做**多 provider 适配（v1 只 DeepSeek；表里留 `provider` 字段作为扩展点）。
- **不做**搜索结果落库/缓存（先看真实用量；缓存会带来过期与隐私问题）。
- **不做** `web_read` 改走三方 Fetch API（用户明确要求抓取能力不变）。
- **不做** search 后自动批量读 N 个页面（token 不可控，交给模型按需决策）。
- **不做**全局兜底 key（没钱就不要偷偷扣系统账户）。

---

## 9. 风险

| 风险 | 应对 |
|------|------|
| 三方 API 改版 | 契约集中在 `deepseek.go` 一处；DTO 与状态码映射单测覆盖 |
| 模型滥用搜索（反复改词重搜） | Description 明写；子 Agent 已有 `CircuitBreakerHook`（同名工具连续失败 3 次即拦）与 `ToolBudgetHook` |
| token 膨胀 | 默认 count=5、摘要 400 字、`need_content` 默认关 |
| 计费不可见 | 日志记 `result_count`，为后续用量面板留字段；暂不做计费统计 |
| 用户把 Key 填错但保存成功 | v1 保存只做格式校验；"测试连接"按钮放 P2（会真实计费一次） |

---

## 10. 待确认决策（评审时拍板）

1. **可见性**：主 Agent 可见（推荐，与 weather 同类）还是子 Agent 专属（与 web_read 一致）？
2. **双开关语义**：卡片开关（按用户，后端可用）+ 工具列表开关（系统级，模型可见）——
   是否接受，还是合并成一个（卡片开关同时控制可见性，需要给 `tools` 表加 user 维度，改动更大）？
3. **`need_content`**：默认 false（推荐，正文交给 `web_read`）还是默认 true（少一次往返，但 token 与延迟都涨）？

## 11. 工作量估算

| 阶段 | 内容 | 估时 |
|------|------|------|
| N1~N3 | 配置底座（domain/repo/service） | 0.5 天 |
| N4~N6 | adapter + tool（含单测） | 0.5~1 天 |
| N7 | API handler + 路由 | 0.2 天 |
| N8 | 前端卡片 | 0.5 天 |
| 收尾 | 提示词、文档、CHANGELOG、`graphify update` | 0.2 天 |
| 合计 | | **2~2.5 天**（可拆 2 个 PR：配置底座 / 工具+前端） |

---

**文档版本**：v1.0
**创建日期**：2026-11-02
**状态**：待评审（确认 §10 三项后进入 TDD）

---

## 12. 落地记录（2026-11-03）

### 12.1 评审拍板

用户于 2026-11-03 确认，三项均按 §10 的建议执行：

| 决策点 | 结论 |
|--------|------|
| 可见性 | **主 Agent 可见**（与 weather 同类）；`web_read` 仍子 Agent 专属，边界未变 |
| 双开关 | 接受双层语义：工具列表开关 = 系统级模型可见性；配置卡开关 = 当前用户后端可用性 |
| `need_content` | 默认 **false**，正文交给 `web_read` |

### 12.2 与方案的偏差（1 处）

**`need_content` 不做成工具参数**，只保留为配置项。

理由：它已经有更好的专用出口——`web_read`。给模型一个"顺手把正文也取回来"的开关，
等于在同一件事上开两条路，容易让模型在 search 里塞全文、把 token 预算吃光；
配置项足够覆盖"我就是想省一次往返"的用户。

其余（表结构、接口路径、状态码文案、预算数值）与方案一致。

### 12.3 实际文件清单

| 层 | 文件 |
|----|------|
| domain | `internal/domain/search/config.go` + `config_test.go` |
| repository | `internal/repository/search/config_repository.go` + 测试 |
| service | `internal/service/search/service.go`、`deepseek.go`、`errors.go` + 3 个测试文件 |
| tool | `internal/service/agent/tools/web_search.go` + 测试 |
| API | `internal/api/web/search_config_handler.go` + 测试；`routes.go` 三条路由 |
| 装配 | `internal/api/wire.go`（service + 工具注册 + handler 注入）、`internal/db/database.go`（AutoMigrate） |
| 提示词 | `internal/agentprompt/content.go`（子 Agent 信息源规则：先搜后读、禁止反复改词重搜） |
| 前端 | `frontend/src/services/search.ts`、`types/api.ts`、`ToolDrawer.vue` 配置卡 |

### 12.4 测试

| 层 | 覆盖 |
|----|------|
| domain | 默认值填充、非法 scheme/主机/条数/超时/服务商、内网地址放行、掩码、CountOr |
| repo | 密文落库（解密可还原、原文不出现）、关闭状态可持久化、不存在返回 nil |
| service | 新建默认启用、留空保持密钥、clear_key、越界不落库、多用户隔离、密文损坏降级 |
| provider | 请求形状（路径/头/字段）、空结果、400/401/402/403/408/413/429/500/504、畸形 JSON、超时、自定义 base_url |
| 格式化 | 空结果文案、标题缺失回落 URL、摘要截断、正文按开关、总预算截断 |
| tool | 元数据、trim、空 query、未配置、无用户身份、7 类错误映射、参数错误上抛 |
| API | 鉴权 401、掩码回显、请求解析、clear_key、校验 400、坏 JSON、删除 |

前端：`vue-tsc --noEmit` 通过；配置卡交互为手测项（保存 → 重开抽屉回显掩码）。

### 12.5 未做（与 §8 一致）

多 provider、结果缓存、`web_read` 改走三方 Fetch、search 后自动批量读页、全局兜底 key
——均为下一版按需评估。
