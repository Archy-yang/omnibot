# 多用户 LarkCLI 授权技术方案

> 2026-10-11 立项。目标：不同登录用户各自授权自己的飞书，feishu_tool 按**当前问答用户**动态
> 执行其本人授权的 lark-cli——授权与用户关联、全流程 DB 驱动、无需任何人手工操作 CLI。
> 可行性调研见 [lark-cli多用户授权调研](../04-参考资料/lark-cli多用户授权调研-2026-10-11.md)
> （profile 机制/非交互授权/存储隔离均已实测）。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | **M1 + M2 已落地（2026-10-11）**；M3（对话内触发）视使用决定 |
| 上游 | [13-Tool与MCP插件系统](./13-Skill与MCP插件系统技术方案.md) §7.5（feishu_tool 现状）、[17-脚本沙箱](./17-脚本沙箱技术方案.md)（§6 安全收口同款纪律）、调研文档（CLI 能力实测） |
| 关联 | v2.2-飞书绑定（user_channels：账号↔飞书身份的消息渠道绑定，与本方案"操作能力授权"互补且独立） |

---

## 1. 这个文档解决什么问题？

现状：feishu_tool 用系统级单一 lark-cli 授权（部署者个人身份），所有用户共享。本方案把
"lark-cli 授权"变成**按用户隔离的、DB 驱动的运行时资源**：

- 授权（device flow）全自动编排：发起 → 验证 URL 送达用户 → 用户浏览器确认 → 收尾落库
- 执行：每次调用按 ctx 里的 user_id 查 DB → 动态 `--profile user-<id>` → 操作用户本人的飞书
- 兼容：无用户上下文/未授权场景有明确语义（回落默认 profile / 引导授权），单用户部署零感知

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| token 本体存储 | **留在 lark-cli 自有存储**（.enc 按 profile 隔离），不入我们的库 | CLI 自管刷新/绑定；对抗式搬库得不偿失。DB 存"路由与状态"（profile/app/授权状态），应用视角全动态 |
| profile 命名 | `user-<userID>`，与 user_id 一一对应 | 可从 user_id 推导，DB 行丢失可重建 |
| device flow 状态 | pending 行存 device_code（短时），**惰性收尾**：状态查询时执行 `--device-code` 短超时轮询 | 无常驻后台任务；用户确认后首次查询即完成 |
| app_secret | AES 加密落库（`enc:` 前缀，crypto 包，mcp_servers 同款） | 安全红线 |
| 授权入口 | v1 Web 设置页（API 形态）；对话内触发留 M3 | 与 v2.2 绑定流的操作习惯一致，实现最薄 |
| 未授权语义 | **不隐藏工具**，执行期返回引导文案（feishu_tool 现有先例） | 用户启动授权后自愈，免重启 |

## 2. 读之前你需要知道什么？

- 13 号 §7.5 feishu_tool：argv 直传/stdin/超时/截断/引导文案；本方案只加"profile 解析"一层，执行纪律不变
- toolcore ctx：`WithUserID`/`UserIDFromContext`（handler 层注入，记忆检索同源）
- 调研文档：`--profile` 按调用指定、`auth login --no-wait --json` + `--device-code` 二段式、`profile add --app-secret-stdin`、`config keychain-downgrade`（服务器场景）——均已实测

## 3. 数据模型

```go
// internal/domain/user/larkcli_auth.go
type LarkCLIAuth struct {
    ID           int64  `gorm:"primaryKey;autoIncrement"`
    UserID       int64  `gorm:"uniqueIndex;not null"`              // 一个用户一份授权(v1)
    ProfileName  string `gorm:"uniqueIndex;size:64;not null"`      // "user-<id>",可由 user_id 推导
    AppID        string `gorm:"size:64;not null"`                  // 自建应用(同租户共享/跨租户各自均可)
    AppSecretEnc string `gorm:"size:1024;not null"`                // AES 密文(enc: 前缀),仅 profile 创建时解密使用
    OpenID       string `gorm:"size:64"`                           // 授权完成的飞书身份(回显)
    UserName     string `gorm:"size:128"`                          // 飞书用户名(回显)
    Status       string `gorm:"size:16;not null;default:pending"`  // pending | authorized | failed
    DeviceCode   string `gorm:"size:128"`                          // pending 期间短时保存,收尾后清空
    VerifiedURL  string `gorm:"size:512"`                          // pending 期间的验证地址(回显)
    CreatedAt / UpdatedAt
}
// TableName: user_larkcli_auths
```

- 状态机：`pending → authorized`（收尾成功）/ `pending → failed`（device code 过期/轮询穷尽）→ 重新发起覆盖 pending。
- `authorized` 行的 DeviceCode/VerifiedURL 必须清空（不留活凭据）。
- 幂等：重复发起 = 覆盖本用户 pending 行（重新拿新 device code）。

## 4. 授权编排服务（`internal/service/larkauth`）

```go
type LarkCLIAuth interface {   // CLI 窄接口,单测 mock
    ProfileAdd(profile, appID, appSecret string) error       // profile add --app-secret-stdin
    LoginStart(profile, domains string) (verifyURL, userCode, deviceCode string, err error)
    LoginComplete(profile, deviceCode string) error           // --device-code(轮询至确认/超时)
    Status(profile string) (LarkStatus, error)                // whoami 解析(openId/userName/授权态)
}
```

流程（service 编排）：

1. **StartAuth(userID, appID, appSecret)**：校验/加密落库 pending 行 → `profile add`（幂等：已存在跳过）→ `LoginStart` 拿 URL/user_code/device_code 回写 pending 行 → 返回 `{verifyURL, userCode}` 给前端展示（含二维码可选）。
2. **PollAuth(userID)**：pending 行存在 → `LoginComplete`（ctx 超时 5s/次）→ 成功：`Status` 取 openId/userName → 行转 authorized（清 device_code）；超时：保持 pending 原样返回"等待确认"；CLI 报过期/拒绝 → failed。
3. **Disconnect(userID)**：`auth logout` + 删行（用户主动断开）。
4. 错误口径：CLI 失败信息透传为可读文案（"授权已过期，请重新发起"），不暴露内部细节。

## 5. API（Web 设置页，JWT）

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/feishu-auth` | 当前用户授权状态（status/app_id 掩码/user_name/open_id/verify_url 仅 pending 时返回） |
| POST | `/api/v1/feishu-auth` | 发起授权 `{app_id, app_secret}`（v1 手填；共享 appId 部署可由 config 提供默认值）→ 返回 verify_url + user_code |
| GET | `/api/v1/feishu-auth/poll` | 惰性收尾 + 状态查询（前端轮询，2s 间隔） |
| DELETE | `/api/v1/feishu-auth` | 断开授权（logout + 删行） |

前端：设置抽屉「飞书授权」区——未授权：表单（app_id/secret）+「连接」按钮 → 展示验证链接（可复制/二维码）+ 轮询至成功；已授权：身份回显 + 断开按钮。

## 6. feishu_tool 改造（执行侧）

```go
// ProfileResolver 窄接口(service/larkauth 实现,工具层依赖)
type ProfileResolver interface {
    ProfileForUser(userID int64) (string, error) // authorized → "user-<id>";否则 ErrNotAuthorized
}

Execute:
  userID := toolcore.UserIDFromContext(ctx)
  profile := ""
  if userID > 0 {
      p, err := resolver.ProfileForUser(userID)
      if err != nil → return 引导文案("尚未授权飞书,请在 设置-飞书授权 完成连接"),不执行
      profile = p
  }
  // userID == 0(无用户上下文,如裸 CLI 调试)→ 不带 --profile,行为同现状
  runFeishuCLI: cmd = bin + (profile != "" ? ["--profile", profile] : []) + args
```

- 其余执行纪律（白名单/`--yes` 拒绝/超时/截断/stdin）**原样不动**。
- ProfileResolver 为 nil（未装配）→ 行为同现状（向后兼容，单用户部署零改动）。
- delegate 派活场景：子 Agent 执行工具的 ctx 是否携带原 user_id **列为实施期验证项**（§9 风险）；若断裂，在子 Agent 执行链补 WithUserID 透传。

## 7. 安全收口

| 面 | 措施 |
|----|------|
| app_secret | AES 加密落库；API 永不回显（仅"已配置"）；日志不落 |
| device_code/verify_url | pending 行短时保存，收尾即清；verify_url 仅返回给发起用户本人（JWT 归属校验） |
| 身份隔离 | profile 按 user_id 命名空间隔离（CLI 实测互不可见）；执行必经 DB 查询，无用户上下文不触碰任何 profile |
| 服务器部署 | 无钥匙串环境执行 `config keychain-downgrade`（官方支持,部署文档记一笔） |
| 审计 | 授权状态变更记结构化日志（user_id/动作/结果,不含 secret） |

## 8. 测试计划（TDD）

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Service | `TestStartAuth_*` | pending 行落库（secret 加密）、profile add 调用、URL/device_code 回写；重复发起覆盖 |
| 2 | Service | `TestPollAuth_*` | 确认成功→authorized+清 device_code；超时→保持 pending；CLI 报过期→failed |
| 3 | Service | `TestDisconnect` | logout 调用 + 行删除 |
| 4 | Tool | `TestFeishuTool_Profile_*` | authorized→argv 前插 `--profile user-<id>`；pending/无行→引导文案且不执行；userID=0→无 --profile（现状）；resolver nil→现状 |
| 5 | API | `TestFeishuAuthAPI_*` | start/poll/delete 契约；secret 永不回显；他人 JWT 无法读他人授权（归属校验天然按 token user） |
| 6 | 回归 | feishu_tool 既有 9 项测试不动全绿 | 执行纪律零回归 |

## 9. 风险与验证项

| 风险 | 处置 |
|------|------|
| delegate 链路 user_id 传递断裂 | 实施期 M2 首项验证;断裂则子 Agent 执行链补透传（toolcore.WithUserID 已有,预期改动小） |
| `--no-wait` JSON 输出字段名未逐字核对 | M1 实施时先实跑一次记录 schema,解析层做成容错(缺字段显性报错) |
| 多用户 + 共享 appId 的配额/权限边界 | 与 §16.1 多用户化批次(Admin 门禁)同批审视,本文档不展开 |

## 10. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| M1 | 表 + larkauth service（mock CLI 全 TDD）+ API + 前端授权区 | §3~§5 + 测试 1~3/5 |
| M2 | feishu_tool 按用户动态 profile + 未授权引导 + delegate ctx 验证 | §6 + 测试 4/6 |
| M3（可选） | 对话内触发授权（主 Agent 引导 + 渠道投递 URL） | 视真实使用决定 |

## 11. 边界与不做

- token 入库/自研飞书 OAuth（绕过 CLI）：不做——CLI 自管是 M5 选型的根基。
- 一个用户多份授权（多租户多身份切换）：v1 一人一授权；真需求出现再扩。
- 授权 domain 细粒度选择：v1 发起时固定 `--domain all`（与现状一致），后续按需加参数。

---

**文档版本**：v1.0
**创建日期**：2026-10-11

---

## 落地记录（M1 + M2，2026-10-11）

- **M1**：`user_larkcli_auths` 表（AutoMigrate）+ `internal/service/larkauth`（Service 编排 +
  `LarkCLI` 窄接口 / `cliReal` 真实实现——argv 直传/超时/--json 宽容解析多候选字段名）+
  `/api/v1/feishu-auth` 四端点（JWT 按 user 隔离,requireUserID 兜底 401）+ 设置抽屉「飞书授权」区
  （发起 → 验证链接 + 2s 轮询自动收尾 → 身份回显/断开）。
- **M2**：`FeishuCLIConfig.Resolver`（nil=现状,向后兼容）——Execute 按 `toolcore.UserIDFromContext`
  查 `ProfileForUser` → argv 前插 `--profile user-<id>`；未授权返回引导文案不执行；wire 装配。
- **§9 风险项实检**：delegate user_id 传递已确认贯通（`sub_agent_runner.go:143` → RunStream →
  withUserID），无需改动。
- 测试：larkauth 4 项（mock CLI）/ handler 4 项 / feishu profile 4 项（stub 脚本）,全量 39 包绿；
  既有 feishu_tool 9 项零回归。

### 收尾分类修复（2026-10-11 晚，实测驱动）

首次真机走流程暴露两个误判（`feishu-auth` 401 误判为另一独立 bug，已同日修）：

1. **成功退出码非零**：实测 `auth login --device-code` 收尾**成功**也以退出码 3 退出（stderr 自打印
   "exited with code 0"）——按"退出码非零=失败"分类会把用户已确认的授权误杀清场。
2. **超时被杀误判失败**：5s 探测超时被吞成字符串错误，`isTimeout` 认不出 → 走 default → failed。
   另实测杀进程后 Run 滞留（5s 封顶漂移到 10.7s，管道被占用所致）。

修复（实测时间数据：确认后**秒级**返回 token；device code `expires_in=600`）：

- `cliReal.run`：超时以 `context.DeadlineExceeded` 类型透出 + `WaitDelay=1s` 防管道滞留
- `PollAuth` 分类重写，**权威判据是 whoami token 就绪，退出码不参与判定**：
  token 就绪 → authorized；探测超时 → 保持 pending（链接仍在，下轮再问）；whoami 瞬时失败 →
  pending 下轮补回显；CLI 明确报错（code 过期/无效）→ failed
- 新增回归测试 5 项（成功但退出码非零 / 超时保持 pending / DeadlineExceeded 类型透出 /
  WaitDelay 有界返回 / 非超时错误可区分），larkauth 9 项全绿
- 前端登录跳转两 bug 同日修复：feishu-auth handler 误读 request ctx（改读 gin.Context）+
  三处硬编码 `/login` 改跟随 BASE_URL（vite base=/chat/）
