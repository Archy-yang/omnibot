# 25 - Persistent Tasks 技术方案：持久任务（周期调度）

> 2026-10-15 立项。[20-路线图](./20-个人管家演进路线图.md) 第三阶段首项。
> 目标:把 Task Runtime 的"一次性派活"升级为可**周期执行**的持久任务——
> "以后每天早上抓一次 AI 资讯发我""每周一关注一下 XX"不再需要每次手动重派。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案已确认(用户 2026-10-15 直接拍板进入开发) |
| 上游 | 08-后台 Agent 任务框架(Task Runtime)、[20-路线图](./20-个人管家演进路线图.md) §5、订阅源触发雏形 |
| 约束 | 不重构 Task Runtime;执行/汇报链路零改动,只在"触发"一层加调度 |

## 1. 这个文档解决什么问题?

现在 `delegate` 派出的是一次性任务:跑完即终,周期性诉求("每天""每周")只能用户每次
手动重派(近期"重抓 AI 订阅源资讯"类任务全部如此)。缺的是:

1. **调度层**:没有"到点自动再派一次"的机制;
2. **持久化**:任务合同(目标/交付物/完成标准)没有跨执行的宿主;
3. **恢复**:服务重启后周期任务不能凭空消失;
4. **管理**:用户看不到/管不了自己有哪些周期任务。

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 调度描述 | **5 字段 cron(本地时区)**,LLM 负责把自然语言换算成 cron | cron 是 LLM 熟悉的表达;"每天早上"具体几点让 Agent 问清或取默认,解析器用 robfig/cron v3(成熟、纯计算) |
| 数据模型 | 新表 `persistent_tasks`(任务合同快照 + 调度状态);每次执行仍是 `agent_tasks` 一行(`source='schedule'` + `schedule_id` 关联) | 执行链路(delegate→run→汇报)零改动;持久任务只管"何时派活" |
| 派活方式 | 到点由 scheduler 调 `SubAgentService.StartTask`(合同从快照反序列化),同一次性任务完全同构 | 复用执行/汇报/通知全链路;实例任务在任务列表里天然可见 |
| 错过触发 | **不追帧**:重启/停机期间错过的触发丢弃,只跑下一次(next_run_at 重建) | 抓资讯类任务旧数据无意义;追帧造成通知洪水 |
| 失败策略 | 连续失败 ≥3 → 自动 `enabled=false`(**不额外发通知**,最后一次失败的正常汇报已到用户) | 频率克制;防故障任务通知轰炸;状态在列表可见 |
| 完成回调 | `SubAgentService` 增加 `SetFinishedObserver(func(*AgentTask))`(终态通知,与既有 Notifier/Publisher 并列) | scheduler 需感知终态维护 fail_streak;单 observer 槽位够用(单调度器) |
| 创建入口 | v1 只走**对话内工具** `manage_persistent_task`(create/list/pause/resume/delete);API 只暴露列表+控制(无创建) | 创建是自然语言活("每周一9点"→cron),LLM 现场换算+确认;前端不做创建表单 |
| 上限 | 每用户持久任务上限 20 | 防滥用;单人量级足够 |
| 时区 | `time.Local`(服务器本地) | 单用户场景;cron 字符串原样存储,解释在运行时 |
| 并发防护 | 触发时 CAS:`UPDATE ... WHERE id=? AND next_run_at=?` 后才派活 | 单实例下是廉价保险,防 ticker 重入/未来多实例 |

## 2. 读之前你需要知道什么?

- 08 号 Task Runtime:`StartTask(ctx, userID, taskSpec, source, notifyTarget, originTurnID)`
  创建并异步执行;汇报走 `SetNotifier`(飞书推送)/`SetCompletionPublisher`(前端 SSE)
- `TaskSpec`(goal/name/deliverables/completion_criteria/background/persona_hint/task_type)
  是任务合同,可 JSON 序列化
- `source` 列已存在(varchar 20),当前取值 chat/web

## 3. 数据模型

```go
// internal/domain/agent/persistent_task.go — TableName: persistent_tasks
type PersistentTask struct {
    ID           int64     `gorm:"primaryKey;autoIncrement"`
    UserID       int64     `gorm:"index;not null"`
    Name         string    `gorm:"size:64"`
    Goal         string    `gorm:"type:text;not null"`
    TaskSpecJSON string    `gorm:"type:text;not null"` // TaskSpec 快照,派活时反序列化
    Cron         string    `gorm:"size:64;not null"`   // 5 字段 cron,本地时区
    Enabled      bool      `gorm:"not null;default:true"`
    NextRunAt    time.Time `gorm:"index;not null"` // 下次触发点(物化,调度器扫它)
    LastRunAt    *time.Time
    LastTaskID   *int64    // 最近一次实例任务(跳转用)
    FailStreak   int       `gorm:"not null;default:0"`
    NotifyTarget string    `gorm:"size:128"` // 传给 StartTask,实例汇报继承
    CreatedAt    time.Time `gorm:"not null"`
    UpdatedAt    time.Time `gorm:"not null"`
}
```

`agent_tasks` 加 `ScheduleID *int64`(index)——实例任务回链持久任务。

## 4. 调度器(`internal/service/agent/persistent_task_scheduler.go`)

- `Start(ctx)`:goroutine + 30s ticker;每轮 `ListDue(now)`(enabled 且 next_run_at ≤ now,
  limit 50)逐个触发;
- 触发流程:反序列化 TaskSpec → CAS 抢占(`UPDATE next_run_at=newNext, last_run_at=now,
  last_task_id 回填 WHERE id=? AND next_run_at=旧值`,影响行数 0 = 已被抢,跳过)→
  `StartTask(ctx, userID, spec, "schedule", notifyTarget, 0)`;
- **启动恢复**:`Start` 时先对 enabled 任务重算 `next_run_at = cron.Next(now)`(停机期间
  错过的触发丢弃,不追帧);
- 依赖窄接口 `TaskStarter`(SubAgentService 已满足),单测 stub。

## 5. 服务层(`persistent_task_service.go`)

- `Create(userID, name, goal, spec, cronExpr, notifyTarget)`:校验 cron 可解析 →
  上限 20 → next_run_at = cron.Next(now) 落库;
- `List(userID)` / `Pause(userID,id)` / `Resume(userID,id)`(resume 重算 next_run_at)/
  `Delete(userID,id)`——全部归属隔离,未命中返回 ErrNotFound;
- `HandleFinished(task *AgentTask)`:task.ScheduleID 为空忽略;成功 → FailStreak=0;
  失败 → streak++,≥3 → Enabled=false(不发通知)。

## 6. 工具与 API

**工具 `manage_persistent_task`**(主 Agent 工具,与 delegate 并列):

- actions:`create`(name/goal/cron 必填,可选 deliverables 等合同字段——与 delegate
  同构)/`list`/`pause`/`resume`/`delete`;
- 工具描述写明 cron 格式与换算纪律("每天早上9点" → `0 9 * * *`;用户没说具体时间
  要确认,禁止编造周期)。

**API**(`internal/api/web/persistent_task_handler.go`):

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/persistent-tasks` | 列表(含 next_run_at/last_task_id/fail_streak/enabled) |
| POST | `/api/v1/persistent-tasks/:id/pause` \| `/resume` | 控制 |
| DELETE | `/api/v1/persistent-tasks/:id` | 删除 |

错误映射:未命中/他人任务 → 404;cron 非法(仅服务层入参,API 不创建)→ 不涉及。

## 7. 前端

- 任务抽屉新增「周期任务」折叠区:名称/cron 人话描述(前端仅展示原 cron + 下次执行
  时间)/状态徽标(运行/已暂停/连续失败)/下次执行时间/最近实例跳转/暂停·恢复·删除按钮;
- `services/persistentTasks.ts` + 类型;空态文案引导"对话里说'以后每天…'即可创建"。

## 8. 测试计划(TDD)

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Repo | CRUD/ListDue | 归属/条件过滤/enabled |
| 2 | Service | Create 校验 | 非法 cron 报错/上限 20/next_run_at 正确(每周一 9 点) |
| 3 | Service | Pause/Resume/Delete | 归属隔离/resume 重算 next/未命中 404 语义 |
| 4 | Service | HandleFinished | 成功清零/失败累加/≥3 自动停用且不发通知 |
| 5 | Scheduler | 触发 | due 任务派活 + CAS 更新/CAS 失败跳过/启动恢复重算 |
| 6 | Tool | manage_persistent_task | create 传参/list 输出/错误透传 |
| 7 | API | 契约 | 401/404/pause·resume·delete |
| 8 | 前端 | vue-tsc + build | 列表与控制按钮 |

## 9. 风险

| 风险 | 处置 |
|------|------|
| LLM 换算 cron 出错(周期不符用户意图) | 工具描述换算纪律 + 创建返回确认文案(下次执行时间)让用户可当场纠正;列表随时可暂停/删除 |
| 停机错过触发 | 明确"不追帧"语义,文档与确认文案均写明 |
| 故障任务通知洪水 | fail_streak ≥3 自动停用 |
| cron 库依赖 | robfig/cron v3 仅做解析/Next 计算,无运行时任务管理(我们自己管状态),依赖面小 |

## 10. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| P1 | 表 + repo + service + 工具 + API | §3~§6 + 测试 1~4/6/7 |
| P2 | 调度器 + 完成回调 + 启动恢复 | §4~§5 + 测试 5 |
| P3 | 前端「周期任务」区 | §7 + 测试 8 |

## 11. 边界与不做

- 不做自然语言 schedule DSL(只收 cron,换算交给 LLM+确认);
- 不做追帧/补偿执行;不做间隔抖动(单人场景无惊群);
- 不做多实例分布式调度(CAS 已是单实例保险);不做 per-user 时区;
- Proactive Butler(基于经历的主动关心)不在本方案,另立项。

---

**文档版本**:v1.1
**创建日期**:2026-10-15

---

## 落地记录(P1+P2+P3,2026-10-15)

- `persistent_tasks` 表(AutoMigrate 已注册;**待下次 `make` 重启才建表**——按用户要求本迭代
  未重启在跑的服务)+ `agent_tasks.schedule_id` 回链列
- `pkg` 之外新增依赖 `robfig/cron/v3`(仅解析/Next 计算,无运行时任务管理;5 字段本地时区)
- 服务层:`PersistentTaskService`(Create 校验 cron/上限 20/next_run_at 计算;Pause/Resume/
  Delete 全归属隔离统一 ErrNotFound;HandleFinished 维护 fail_streak,≥3 自动停用不发通知)
- 调度器:30s tick + `ListDue` + **CAS 抢占**(`ClaimDue` WHERE next_run_at=旧值)→
  `StartTask` 派实例 → 回填 last_task_id;启动恢复重算 next_run_at,**不追帧**
- 关键修正(方案评审中抓出):实例任务的 `source` **继承创建渠道**(feishu/web)而非标
  "schedule"——`notifyCompleted` 只对 `source=feishu` 主动推送,标 schedule 会让汇报静默丢失;
  `persistent_tasks` 落 `source/notify_target` 渠道快照,LLM 不经手(同 delegate 的 ctx 注入)
- 终态观察:`SubAgentService.SetFinishedObserver`(notifyCompleted 加载终态任务后触发,
  成功/失败/取消全到达;与既有 Notifier/Publisher 并列,单槽位)
- 工具 `manage_persistent_task`(create/list/pause/resume/delete):cron 换算纪律写进工具
  描述(用户没说具体时间先确认,禁止编造);创建确认文案带下次执行时间供当场核对
- API `GET/POST/DELETE /api/v1/persistent-tasks[...]`(列表视图 DTO 不泄露 TaskSpecJSON)
- 前端:任务抽屉列表页顶部「周期任务」区(仅在有数据时展示):名称/周期/下次执行/状态徽标
  (含"连续失败已停用")/暂停·恢复·两段式确认删除;`persistentTasks.ts` service
- 测试:service 6 组(创建校验/归属/HandleFinished/调度触发/恢复/工具全链路)+
  handler 契约(404 归属隔离/401/500),全量 Go 包绿;vue-tsc 干净,vite build 通过
