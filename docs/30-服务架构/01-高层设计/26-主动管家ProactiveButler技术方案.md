# 26 - Proactive Butler 技术方案：主动管家

> 2026-10-16 立项。[20-路线图](./20-个人管家演进路线图.md) 第三阶段第二项。
> 目标:让管家**基于近期经历适时开口**——事项将到期、未决承诺搁置太久、近况值得跟进时
> 主动提一句;**不是**每天定时的机械推送(那是 25 号持久任务的领地)。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 方案评审中 |
| 上游 | 21号经历层(信号源)、25号持久任务(调度/通知先例)、14号订阅源(定时推送雏形)、[20-路线图](./20-个人管家演进路线图.md) §5 |
| 红线 | **默认关闭**(用户显式开启才工作);频率克制;可一键关闭;20号 §5 原文"授权范围内、频率克制、可一键关闭" |

## 1. 这个文档解决什么问题?

现在所有能力都是被动的:问了才答、派了才做、配了周期才跑。经历层(事项/loop/记忆)里
躺着大量"该主动提一句"的信号——复查日期临近、承诺的资料还没给、上周的事说好跟进——
但没有机制让管家开口。缺的是:

1. **信号收集与裁决**:哪些事值得主动提?谁来决定"今天值不值得开口"?
2. **克制机制**:怎么保证不打扰(这是主动类功能的生死线)?
3. **授权与关闭**:用户怎么开、怎么关、怎么看到它主动说过什么?

### 1.1 关键设计决策

| 决策 | 结论 | 理由 |
|------|------|------|
| 触发形态 | **每日一次「主动关心扫描」**:到点收集信号 → LLM 裁决"今天有没有真正值得提的一件事" → 常态**不发** | 信号驱动而非模板驱动;与"机械提醒"划清界限——没有值得说的就沉默,复用沉淀管线"常态空数组"哲学 |
| 信号源 v1 | ①活跃事项的 state_desc(含日期/临近信号) ②未决 loop(open > 3 天) ③近 7 天记忆概览 | 三类接口现成(ListActiveByUserID/ListOpenLoops/检索);v1 不做更多 |
| 裁决方式 | 单次 LLM 调用输出 `{send: bool, content, kind, dedup_key}`;提示词红线"宁可漏,不可扰;没有就 send:false" | 裁决与生文一次完成;LLM 是唯一合理的"值不值得提"判断者 |
| 频率克制 | **每日上限 1 条** + 同一 dedup_key 7 天冷却 + 默认时刻 09:30(可配,夜间时段拒绝) | 单人场景的克制底线;dedup 防同一事项反复唠叨 |
| 授权 | `users.proactive_enabled` **默认 false** + `proactive_hour`(默认 9);设置页开关 | 20 号红线:主动行为必须显式授权;关 = 立即静默,无残留 |
| 送达 | 飞书卡片优先(`user_channels` 查 open_id,SendCard 先例);未绑定渠道 → 仅落库(历史可见,不丢) | 用户主用飞书;web 无主动推送面,v1 落库即达 |
| 记录 | 新表 `proactive_notes`(信号类型/内容/溯源 matter_id/dedup_key/状态/反馈)+ 消息 Kind=proactive | 主动说过什么必须可回看、可反馈;溯源到事项 |
| 反馈 v1 | 每条消息尾注"不需要时回复'不用再提',我从此不再说这件事"——用户在对话里说,主 Agent 自然语言处理;**主题级静音(dedup_key 精确禁言)留 v1.1** | v1 不加新工具/新 API;7 天冷却 + 每日 1 条已兜住体验,主题静音等真实使用反馈 |
| 架构 | `internal/service/butler`(扫描服务)+ 每小时 ticker(到点且当日未跑才执行,复用 25 号调度纪律) | 与 digest pipeline/scheduler 同款后台模式 |
| LLM 配置 | 复用用户级配置解析(25/24 号 eval-memory 同款通路) | 不重复建设 |

## 2. 读之前你需要知道什么?

- 21 号:Matter(state_desc 覆写式状态)、loop(open/closed)、记忆检索
- 25 号:调度器纪律(CAS/tick/启动恢复)、`BuildReportInstruction` 主 Agent 转述先例、
  飞书 SendCard 卡片推送、`user_channels` 渠道绑定
- 沉淀管线提示词的"常态空数组"纪律(§7 沉淀)——本方案的"常态不发"同源

## 3. 数据模型

```go
// internal/domain/butler/proactive_note.go — TableName: proactive_notes
type ProactiveNote struct {
    ID         int64  `gorm:"primaryKey;autoIncrement"`
    UserID     int64  `gorm:"index;not null"`
    Kind       string `gorm:"size:32;not null"`  // matter_due / loop_stale / recent_followup
    Content    string `gorm:"type:text;not null"` // 管家口吻的主动消息
    MatterID   *int64 `gorm:"index"`              // 溯源(可空)
    DedupKey   string `gorm:"size:128;not null"`  // 如 "matter:42"/"loop:101"——冷却与 v1.1 静音的锚
    Status     string `gorm:"size:20;not null"`   // delivered / skipped(裁决通过但冷却中/超日限)
    Feedback   string `gorm:"size:20"`            // positive / negative(空=未反馈)
    CreatedAt  time.Time `gorm:"not null"`
    DeliveredAt *time.Time
}
```

`users` 表加两列:`proactive_enabled bool`(default false)、`proactive_hour int`(default 9)。

## 4. 扫描服务(`internal/service/butler`)

每日流程(到点触发,当日已跑则跳过):

1. **收信号**:
   - 活跃事项:标题 + state_desc 全文(日期信号由 LLM 识别,不做正则)
   - 未决 loop:open 且 open 超过 3 天的
   - 近 7 天记忆:fact/loop 内容清单(截断)
2. **LLM 裁决**(单次调用,系统提示词 = 主动关心纪律):
   - 输出 JSON:`{"send": false, "reason": "..."}` 常态;或
     `{"send": true, "kind": "...", "dedup_key": "...", "content": "..."}`(content 用管家口吻,
     2-3 句以内,只提一件事,给出可行动的建议或一个具体问题)
   - 红线:宁可漏,不可扰;不值得就沉默;禁止编造日期/事实(只能引用信号输入里的内容)
3. **克制闸门**(代码层,不信 LLM):
   - 当日已发过 → skip
   - 同 dedup_key 7 天内有 delivered → skip
4. **落库 + 送达**:写 proactive_notes(delivered)→ 飞书卡片(尾注反馈提示);
   无渠道绑定 → 仅落库。消息同时落 messages(Kind=proactive),历史可还原。

## 5. API 与前端

| 项 | 说明 |
|----|------|
| 开关 API | `GET/PUT /api/v1/proactive-settings`(enabled/hour)——归属隔离 |
| 前端 | 设置抽屉「主动关心」区:开关 + 时刻选择 + 最近主动记录列表(内容/时间/事项跳转) + 一键关闭说明 |
| 空态文案 | "默认关闭。开启后,管家每天最多主动找你一次,只在真有事时开口。" |

## 6. 测试计划(TDD)

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Repo | notes CRUD/冷却查询 | dedup 7 天窗口/当日计数 |
| 2 | Service | 信号收集 | 三类信号正确组装/空快照不崩 |
| 3 | Service | LLM 裁决 | send:false 零落库/send:true 落库/JSON 容错 |
| 4 | Service | 克制闸门 | 当日已发 skip/dedup 冷却 skip/代码闸门优先于 LLM |
| 5 | Service | 开关 | enabled=false 全程 no-op/hour 错位不触发 |
| 6 | API | settings 契约 | 401/归属/校验(hour 0-23) |
| 7 | 前端 | vue-tsc/build | 开关与列表 |

## 7. 风险

| 风险 | 处置 |
|------|------|
| LLM 乱开口(噪声/编造) | 提示词红线 + 代码层日限/冷却双闸门 + 默认关闭 + 每条可反馈 |
| 骚扰感知 | 每日上限 1 是硬编码常量(非配置),v1 不给"放开频率"的口子 |
| 无事硬找事 | "常态不发"哲学:send:false 是期望输出,报告质量用 E1 式金样本评估(后续可挂 24号 M3 同款回放) |
| 静默时段漏配 | 代码层拒绝 22:00-08:00 触发,与配置无关 |

## 8. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| N1 | 表+开关列+信号收集+LLM 裁决+克制闸门+落库 | §3~§4 + 测试 1~5 |
| N2 | 送达(飞书/仅落库)+ 每日调度接线 + 消息还原 | §4 + 测试 6 部分 |
| N3 | 设置页「主动关心」区 + settings API | §5 + 测试 6~7 |

## 9. 边界与不做(v1)

- 不做主题级静音(dedup_key 禁言,设计已预留,v1.1 视反馈)
- 不做多条/高频档位(每日 1 条硬编码);不做多用户批量优化
- 不做主动**执行**(只开口建议,动作仍由用户确认后走既有链路)
- 不做 webhook/邮件等其他通道;不做即时事件触发(如事项刚更新就提——冷却机制 v1 不含)

---

**文档版本**:v1.0
**创建日期**:2026-10-16
