# 27 - lark-cli 授权状态 DB 物化技术方案

> 2026-10-18 立项。19 号(用户级 lark-cli 授权)的部署形态演进;20 号路线图贯穿要求"多用户"的地基。
> 目标:把 lark-cli 的授权状态从**机器本地文件**变为 **DB 唯一事实源**——多机部署数据一致,
> 多用户凭据物理解耦,任何实例都能执行任何用户的飞书调用。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案评审中 |
| 上游 | 19 号(多用户 LarkCLI 授权)、[26 号审计](../../..) 架构回顾、[lark-cli 存储坑位实测](#2-调研结论已实测) |
| 前置 | PR#23(容器化修复:Linux CLI 进镜像 + 双 volume)已落地——单机形态已稳定,本方案是多机正解 |

## 1. 这个文档解决什么问题?

19 号的设计里,DB(`user_larkcli_auths`)只存 **user↔profile 映射 + 加密 secret 留档 + 状态机**;
token 本体始终在 lark-cli 自己的本地存储。2026-10-18 实测确认三处位置:

| 平台 | profile 配置 | token 实体 |
|------|-------------|-----------|
| macOS | `~/.lark-cli/config.json` | **系统 Keychain**(可 `config keychain-downgrade` 降级为文件) |
| Linux | `~/.lark-cli/config.json` | **XDG 数据目录** `~/.local/share/lark-cli/*.enc` |

后果:
1. **多机部署堵死**:实例 A 授权,实例 B 无 token——飞书调用在 B 上必挂(容器重启丢 token 已实际发生过,PR#23 以双 volume 止血单机形态)
2. **凭据与用户物理解耦失败**:同一台机器上所有用户的凭据躺在同一个目录,隔离只靠 `--profile` 参数(服务是唯一调用方,够用但不是边界)
3. **备份/迁移靠拷文件**:绕过 DB 的隐性状态,与"DB 为事实源"的全仓纪律相悖

### 1.1 调研结论(已实测,2026-10-18)

| 结论 | 证据 |
|------|------|
| **HOME 重定向 100% 有效** | `HOME=/tmp/x lark-cli profile list` 返回空;CLI 状态目录完全跟随 `$HOME` |
| CLI 无 token 直传/env 注入口 | 全量翻 `--help` 与二进制 strings:`LARKSUITE_CLI_USER_ACCESS_TOKEN` 存在但语义未文档化,不赌 |
| token 交换成功≠落盘 | PR#23 期间实测:授权 200 后无任何 token 文件写入 volume(XDG 路径未挂载),重启即失 |
| `ProfileAdd` 幂等遮蔽已修 | PR#28 前身:同 profile 重复 add 不更新 secret → 已改删除重建 |

## 2. 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 物化粒度 | **整目录 blob**(两个状态目录打包),不解析内部格式 | CLI 格式无文档且随版本漂移;黑盒同步对 CLI 升级免疫。排除 `locks/`(锁文件)与 `logs/`(日志) |
| 事实源方向 | DB 为**唯一**事实源;执行 = hydrate → exec → write-back | token 会在执行中刷新(refresh token 轮转),**只读物化会导致他机拿到已轮转的旧 refresh token**,必须回写 |
| 并发控制 | per-user 执行互斥(单机 mutex)+ **version CAS**(多机 last-writer-wins + 冲突日志) | 单人场景同用户并发执行低频;刷新轮转冲突是边缘 case,CAS+告警足够,不引分布式锁 |
| 执行包装 | 统一 `LarkExecutor`:hydrate → `HOME=<dir> lark-cli ...` → write-back,三处调用点(feishu_tool/feishudoc/larkauth)全走它 | 漏一处 = 该路径绕过物化,token 分叉 |
| 加密 | blob 复用全局 AES 密钥(`crypto`,与 LLM 配置同源) | 凭据数据,安全红线要求加密存储;不引入新密钥体系 |
| 临时目录 | `/tmp/lark-homes/<userID>/`,0700,进程内 LRU 清理 | 隔离边界:每用户只见自己的 HOME;不落持久盘 |
| 授权流 | StartAuth/PollAuth 成功/Disconnect 后**立即 snapshot** | 授权动作是 blob 变化的确定性锚点;执行中刷新靠 write-back |
| 迁移 | 上线时把现 volume 状态(当前单机)做成 user-1 首版 blob,此后 volume 只作回退保险 | 平滑切换;单机 volume 与 DB 短暂并存,以 DB 为准 |

## 3. 数据模型

```go
// internal/domain/user/lark_cli_state.go — TableName: lark_cli_states
type LarkCLIState struct {
    UserID    int64     `gorm:"primaryKey"`          // 与 user_larkcli_auths 1:1
    StateBlob []byte    `gorm:"type:bytes;not null"` // tar.gz 后 AES 加密的两目录快照
    Version   int64     `gorm:"not null;default:1"`  // 每次写回 +1;多机 CAS 锚
    UpdatedAt time.Time `gorm:"not null"`
}
```

Blob 结构(打包清单):
- `lark-cli/config.json`(`~/.lark-cli/config.json`)
- `lark-cli/cache/**`(`~/.lark-cli/cache/`)
- `lark-xdg/**`(`~/.local/share/lark-cli/`,token 实体)
- 排除:`locks/`、`logs/`、`._*`(macOS AppleDouble 垃圾)

## 4. 服务设计(`internal/service/larkhome`)

```go
// LarkHomeManager blob 的物化与回写(唯一读写 lark_cli_states 的组件)。
type LarkHomeManager interface {
    // Hydrate 把用户的 blob 物化到 /tmp/lark-homes/<uid>/(幂等:目录已存在且版本一致则跳过)。
    Hydrate(userID int64) (homeDir string, err error)
    // SyncBack 执行后回写:重打包临时目录 → 与 DB version CAS → 不一致则重试一次 → 仍失败记冲突告警(本地已刷新,下次 hydrate 会覆盖)。
    SyncBack(userID int64) error
    // Snapshot 立即把指定用户的临时目录状态写 DB(授权动作后调)。
    Snapshot(userID int64) error
}

// Executor 统一执行入口(替换散落三处的 exec.Command(bin, ...))。
type Executor interface {
    Run(ctx context.Context, userID int64, args ...string) (stdout string, err error)
}
```

Executor 流程:`per-user mutex → Hydrate → exec(HOME=临时目录, argv 直传) → SyncBack → 返回`。
执行失败也 SyncBack(部分路径会写日志/缓存);Hydrate 失败直接返回错误(无凭据可用)。

三个调用点改造:
| 调用点 | 现状 | 改造 |
|--------|------|------|
| `feishu_tool.go`(主 Agent 飞书全域工具) | 自持 exec | 换 Executor |
| `feishudoc.go`(档案同步 runner) | 自持 exec | 换 Executor |
| `larkauth/larkcli.go`(授权编排) | 自持 exec | 换 Executor(授权动作后由 Service 调 Snapshot) |

## 5. 测试计划(TDD)

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Manager | Hydrate/SyncBack 往返 | 物化目录与 blob 内容一致(含 XDG 路径);排除 locks/logs/._* |
| 2 | Manager | 版本 CAS | version 不匹配时重试;两次失败记冲突且不静默覆盖 |
| 3 | Manager | Hydrate 幂等 | 同版本重复 Hydrate 不重复解包 |
| 4 | Executor | HOME 注入 | 子进程收到正确 HOME;argv 直传不经 shell |
| 5 | Executor | 串行 | 同用户并发 Run 串行化;不同用户并行 |
| 6 | Service | 授权动作锚点 | StartAuth/PollAuth 成功/Disconnect 后 Snapshot 被调 |
| 7 | 集成 | 真实 CLI 往返 | profile add → hydrate 到新目录 → 新 HOME 下 profile list 可见 |

## 6. 风险

| 风险 | 处置 |
|------|------|
| write-back 丢失 → 他机拿已轮转 refresh token | Executor 强制 SyncBack(失败也调);CAS 失败记冲突告警,下次 hydrate 以胜者为准 |
| blob 膨胀 | 排除 locks/logs;cache 上限观察(当前实测 KB 级);超限告警 |
| 临时目录残留 | 进程退出清理 + Hydrate 幂等覆盖;/tmp 重启自清 |
| CLI 升级改内部格式 | 黑盒 blob 不受影响;仅 CLU 版本锁策略(1.0.94)需联动评估 |
| 多机同用户高频并发 | 单人场景不成立;若未来出现,升级为 per-user DB 行锁(方案预留 version CAS 已是前置) |

## 7. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| A2-1 | 表 + LarkHomeManager(Hydrate/SyncBack/Snapshot)+ 打包/加密 | §3~§4 + 测试 1~3 |
| A2-2 | Executor + 三调用点接线 + per-user 串行 | §4 + 测试 4~5 |
| A2-3 | 授权流锚点 + 迁移脚本(现 volume → 首版 blob)+ 集成验证 | 测试 6~7 |
| A2-4(可选) | 多实例部署验证(两容器共 DB 互踢执行) | 部署文档 |

## 8. 边界与不做(v1)

- 不解析/改写 CLI 内部格式(纯黑盒 blob)
- 不做跨用户凭据共享;不做 CLI 多版本共存
- 不做实时双向同步(执行即同步,无后台定时对账)
- 不动 19 号的映射/状态机表(两表并存,职责分明)

---

**文档版本**:v1.0
**创建日期**:2026-10-18
