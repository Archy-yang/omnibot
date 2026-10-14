# 23 - Document Integration 技术方案：个人经历同步到飞书文档

> 2026-10-14 立项。目标：把 21 号的个人经历层（Journal 时间轴 / Matter 档案）一键同步为
> 飞书文档——自动创建、增量更新、事后纠正后可再同步。
> [20-路线图](./20-个人管家演进路线图.md) 第二阶段第一项。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案评审中 |
| 上游 | [21-个人经历层](./21-个人经历层Journal与Archive技术方案.md)（数据与视图）、[19-多用户LarkCLI授权](./19-多用户LarkCLI授权技术方案.md)（按用户身份执行）、feishu_tool 执行纪律（13 号 §7.5） |
| CLI 能力实测 | `docs +create --doc-format markdown --content -`（stdin,免转义）；`docs +update --command overwrite`（全量覆盖）与 `append`（追加）均支持；`--parent-position my_library` 可落"我的空间" |

---

## 1. 这个文档解决什么问题？

个人经历现在只在 OmniBot 内可见（JournalDrawer）。用户真实诉求是**在飞书里持续沉淀自己的记录**——
旅行日记、工作日志、项目记录要能被导出、分享、长期留存。缺的是：

1. 没有从 Matter/Journal 到飞书文档的通道；
2. 没有"增量更新"语义（同步过的档案，新记忆进来后文档要能跟上）；
3. 没有幂等保障（重复同步不该造出一堆重复文档）。

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 文档与档案的绑定 | matters 表新增 `feishu_doc_token`/`synced_at` 列；**首次同步 create,再次同步 update overwrite** | token 存在 = 幂等,永不重复建档 |
| 增量语义 | **全量覆盖**（渲染当前档案全景为 markdown,overwrite） | 最简单可靠;append 会随纠错产生脏历史。文档头部固定标注"由 OmniBot 自动维护,手动编辑会被下次同步覆盖"——修正请回 OmniBot 纠正后重新同步（源头正确原则） |
| 文档归属位置 | `--parent-position my_library`（用户自己的空间） | 无需选文件夹;v1 不做位置选择 |
| 同步触发 | v1 = **Web 按钮**（档案详情"同步到飞书"）；对话内工具/定时自动同步不做 | 按需手动最符合"导出"心智;自动化放第三阶段 Persistent Tasks 一并考虑 |
| 执行身份 | 复用 19 号：`larkauth.ProfileForUser(userID)` → `--profile user-<id>` | 用户自己的授权、自己的文档空间;未授权 → 引导文案 |
| 渲染格式 | markdown（`--doc-format markdown`,内容走 stdin 免转义） | CLI 实测支持;记忆内容天然是文本 |
| 执行纪律 | argv 直传不经 shell / 超时 / 输出截断 / 不碰 `--yes` 类高危 | 同 feishu_tool(13 号 §7.5) |

## 2. 读之前你需要知道什么？

- 21 号：Matter 详情（StateDesc + 挂靠记忆时间线）与 Journal 条目结构;`ListByMatter`/`ListJournal` 已就绪
- 19 号：`ProfileForUser`（authorized → `user-<id>`,否则 ErrNotAuthorized）与 AES 授权存储
- CLI 实测：`+create`/`+update` 支持 markdown + stdin;输出 JSON（含文档 URL/token）

## 3. 数据模型改动

```go
// internal/domain/memory/matter.go — Matter 新增:
FeishuDocToken string `gorm:"size:128"`  // 非空 = 已建档,再同步走 overwrite
FeishuDocURL   string `gorm:"size:512"` // 回显给前端直接打开
SyncedAt       *time.Time            // 最近同步时间
```

存量不回填（空 = 从未同步,首次走 create）。

## 4. 同步服务（`internal/service/feishudoc`）

```go
type LarkDocRunner interface {   // CLI 窄接口(单测 stub)
    CreateDoc(ctx, profile, title, markdown string) (docToken, docURL string, err error)
    OverwriteDoc(ctx, profile, docToken, markdown string) error
}
type ProfileSource interface {   // 19号 ProfileResolver 复用
    ProfileForUser(userID int64) (string, error)
}
```

- `SyncMatter(ctx, userID, matterID)` 流程：
  1. 归属校验（matterRepo.GetByID,nil → ErrNotFound）
  2. `ProfileForUser`（未授权 → ErrNotAuthorized,handler 映射引导文案）
  3. 渲染 markdown：`# {title}` + 当前状态段 + `> 由 OmniBot 自动维护…` 提示行 + 演进时间线
     （`ListByMatter`,每条 `- [日期] 内容`;含 pinned 标记）
  4. token 空 → `CreateDoc`（title = matter title）并回写 `feishu_doc_token/url/synced_at`；
     非空 → `OverwriteDoc` + 更新 `synced_at`
- 执行实现：argv 直传 `lark-cli --profile <p> docs +create/+update ... --content -`（stdin 传正文）、
  60s 超时、输出截断、JSON 宽容解析（URL/token 多候选字段名）。
- **渲染是纯函数**（`RenderMatterMarkdown(matter, items)`）——单测不需要 CLI。

## 5. API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/matters/:id/sync-doc` | 同步档案到飞书;返回 `{doc_url, synced_at, created: bool}` |

错误映射：ErrNotFound → 404；ErrNotAuthorized → 401 + 文案"尚未完成飞书授权,请在 设置-飞书授权 中连接"；
CLI 失败 → 502 + 可读原因。

## 6. 前端

- 档案详情页新增「同步到飞书」按钮：未建档 → 显示"创建文档"；已建档 → 显示"更新文档（上次同步 X）"；
  成功后展示文档链接（新窗口打开）+ Toast。
- 21 号 MatterSummary/MatterDetail 类型与后端列同步扩展（doc_url/synced_at 只读展示）。

## 7. 测试计划（TDD）

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Service | RenderMatterMarkdown | 标题/状态/提示行/时间线日期与 pinned 标记/空档案 |
| 2 | Service | SyncMatter | 首次 create+回写 token/url/synced_at;再次 overwrite 不重复建档;未授权/未找到/CLI 失败语义 |
| 3 | API | sync-doc | 契约与错误映射(404/401/502);归属隔离 |
| 4 | 前端 | vue-tsc + build | 按钮状态与回显 |

## 8. 风险

| 风险 | 处置 |
|------|------|
| 覆盖用户在飞书侧的手动编辑 | 文档头部固定警示行（§1.1）;语义上文档是导出镜像不是协作面 |
| CLI JSON 输出字段漂移 | 多候选字段宽容解析 + 缺字段显性报错（19 号同款纪律） |
| 大档案同步耗时 | 60s 超时 + 前端 loading;档案量级（百条记忆）远小于风险线 |

## 9. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| F1 | 表列 + feishudoc service + API + 档案详情同步按钮 | §3~§6 + 测试 1~4 |
| F2（可选,视使用） | Journal 整体时间轴同步 / 对话内同步工具 / 定时自动同步 | 视 F1 真实使用决定 |

## 10. 边界与不做

- 不做文件夹/知识库位置选择（固定 my_space）；不做双向同步（飞书侧编辑不回流）。
- 不做多媒体（图片/附件）同步；v1 纯文本 markdown。
- 不做权限分享设置（文档建在用户自己名下,分享由用户在飞书侧操作）。

---

**文档版本**：v1.1
**创建日期**：2026-10-14

---

## 落地记录（F1，2026-10-14）

- matters 加 `feishu_doc_token/feishu_doc_url/synced_at` 列（AutoMigrate,DB 实查确认）
- `feishudoc` service：`RenderMatterMarkdown` 纯函数（警示行/当前状态/档案状态/时间线,
  无事件时间回落 created_at,pinned ★ 标记,空档案"(暂无记录)"）+ `SyncMatter`
  （首次 create 并回写 token/url/synced_at;再次 overwrite 同一文档,`createCnt` 恒 1——幂等实测）
  + 真实 CLI runner（`--profile` 按用户/stdin 传正文/60s 超时 + WaitDelay/JSON 多候选宽容解析,
  缺 token 显性报错）
- API `POST /matters/:id/sync-doc`：404 未找到 / **403 未授权**（刻意避开 401——前端拦截器对
  401 会清 token 跳登录页,语义不符）/ 502 CLI 失败透传
- MatterSummary/MatterDetail 回显 `doc_url/synced_at`；档案详情「同步到飞书」按钮：
  未建档 → 创建;已建档 → 更新（覆盖前二次确认）+ 文档链接与上次同步时间回显
- 测试 3 项,全量 40 包绿;vue-tsc 干净,vite build 通过
- F2（Journal 整体同步/对话内工具/定时同步）未开工,视 F1 真实使用决定
