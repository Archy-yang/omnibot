# Skill 技能系统技术方案

> 2026-10-11 立项。兑现 13 号方案正名修订时的"Skill 概念留白"：**Skill = 用户可定义的、
> 按需注入的、可带脚本的程序性知识包**（与记忆"事实"、tool"单点能力"、prompt"管家规则"互补）。

## 文档信息

| 项 | 内容 |
|----|------|
| 版本 | v1.0 |
| 状态 | 已确认（2026-10-11 设计对齐；S1 待实施） |
| 上游 | [13-Tool与MCP插件系统技术方案](./13-Skill与MCP插件系统技术方案.md)（skill 概念留白出处）、[11-Prompt管理技术方案](./11-Prompt管理技术方案.md)（section 化/provider 机制）、[17-脚本沙箱技术方案](./17-脚本沙箱技术方案.md)（脚本执行底座） |

---

## 1. 这个文档解决什么问题？

个人助手需要沉淀**程序性知识**（"怎么做某类事"的流程），现有三个机制都覆盖不了：

| 现有机制 | 天花板 |
|---------|--------|
| 记忆（M6~M8） | 记事实不记流程，复现时漏步骤、口径漂移 |
| Prompt section | 用户不可定义；且每段常驻上下文——单一长期对话模型下不可持续膨胀 |
| Tool/MCP | 单点能力，无编排 |

Skill = **name/description（目录，常驻）+ instruction（指令正文，按需加载）+ files（脚本/资源，按需执行）**。
核心收益：① 上下文经济（目录常驻几百 token，正文跟任务走）；② 用户在线教助手做事（Web CRUD）；
③ **助手自生成**（Agent 经工具落库，"把充电分析做成技能"一句话沉淀流程）。

### 1.1 关键设计决策（评审对齐记录）

| 决策 | 结论 | 理由 |
|------|------|------|
| 存储形态 | **DB 表为单一事实源**（文件目录方案作废） | 在线编辑/在线添加/Agent 自动生成都要求落库；对齐 mcp_servers M3 先例（config.yaml→DB） |
| 目录注入 | 目录段由 SkillService 提供（provider 动态段），**不在 prompt 常量里手写清单** | 11 号 §3.5 分工原则——prompt 里的清单副本必然脱节（派活规则 weather 事故同源教训） |
| Agent 生成的技能 | **默认 enabled=false**，用户审阅后手动开启 | 助手能写，但不能未经审阅就把内容加进自己的指令面（防隐藏规则） |
| skill name | **创建后不可变**（更新只改 description/instruction/files/enabled） | name 是目录 key 与 load_skill 入参，可变会破坏引用稳定性 |
| 加载机制 | 内存缓存 + CRUD 即失效重建（复用 ToolService ApplyTo 模式） | 启停/编辑即时生效，无需重启 |

## 2. 读之前你需要知道什么？

- 11 号方案的 section 化组装（order/scope、`Provider` 动态段——本方案是其**首个生产使用**）
- 13 号方案的 tools 表/ToolService 模式（seed、启停、ApplyTo 重建）
- 17 号方案的 `sandbox_run`（S2 脚本执行底座）与 Files 注入

## 3. 数据模型

```go
// internal/domain/skill/skill.go
type Skill struct {
    ID          int64  `gorm:"primaryKey;autoIncrement"`
    Name        string `gorm:"uniqueIndex;size:64;not null"` // 目录 key,创建后不可变
    DisplayName string `gorm:"size:64"`                      // 面向用户的中文名
    Description string `gorm:"size:512;not null"`            // 一句话概述(目录注入给 LLM 的匹配依据,写清"何时用")
    Instruction string `gorm:"type:text"`                    // 指令正文(Markdown,按需加载;上限 32KB)
    Source      string `gorm:"size:16;not null;default:manual"` // manual | agent
    Enabled     bool   `gorm:"not null"`                     // false = 不进目录、load_skill 404
    UserID      *int64 `gorm:"index"`                        // NULL=共享(单用户期恒 NULL,对齐 mcp_servers 预留)
    CreatedAt / UpdatedAt
}

type SkillFile struct {
    ID       int64  `gorm:"primaryKey;autoIncrement"`
    SkillID  int64  `gorm:"index;not null"`
    Path     string `gorm:"size:256;not null"` // 相对路径,如 analyze.py / reference.md
    Content  string `gorm:"type:text"`         // 文本内容(上限 64KB/文件)
}
// 唯一约束 (skill_id, path);级联删除随 skill
```

约束：
- `name` 全局唯一，格式 `^[a-z0-9-]{2,64}$`（目录 key 风格）；`DisplayName` 允许中文。
- `instruction` ≤ 32KB、`Description` ≤ 512B、单文件 ≤ 64KB、每 skill ≤ 10 个文件（写入时校验，超限 400——防目录注入与按需加载被单技能撑爆）。
- 无 seed：v1 无内置技能（管家规则留在 prompt section），表空 = 目录段不注入。

## 4. SkillService（`internal/service/skill`）

```go
type SkillService struct {
    repo   SkillRepository        // CRUD
    mu     sync.RWMutex
    catalog []CatalogEntry        // 内存缓存:enabled 技能的 name/display_name/description
}

// CRUD(全部落库后 invalidate 缓存)
CreateSkill / UpdateSkill / DeleteSkill / ListSkills / GetSkill(name or id)

// 目录提供(供 prompt provider 调用,R Lock)
Catalog() []CatalogEntry          // enabled 的 {name, display_name, description}
LoadInstruction(name string) (string, []FileMeta, error)  // 正文 + 文件清单
LoadFile(name, path string) (string, error)               // 文件内容(沙箱 Files 用)

// Agent 自助(S1.5)
SaveSkillByAgent(name, description, instruction string, files []FileInput) (int64, error)
// → source=agent, enabled=false;name 冲突报错(提示换名),不覆盖
```

## 5. Prompt 注入与工具

### 5.1 技能目录段（`skill_catalog`，order 95，scope main）

- 用 11 号的 **Provider 动态段**：组装时调 `SkillService.Catalog()` 渲染，无 enabled 技能则不注册该段。
- 注入文案（骨架）：

```
== 技能目录==
你可以加载以下技能获取特定任务的详细做法(调 load_skill,传 name):
- 充电分析(charge-analysis):分析飞书充电记录表的电耗与成本,按固定口径汇报
- ...
当前目录没有相关技能时,按常规方式处理;用户要求把某个流程沉淀为技能时,用 save_skill 落库(S1.5)。
```

### 5.2 `load_skill` 工具（builtin，主 Agent 可见，MainVisible=true）

- 参数：`{name: string, file?: string}`
- 无 `file`：返回 instruction 全文 + 文件清单（path 列表，提示"需要脚本内容时再带 file 参数"）
- 带 `file`：返回该文件内容（传 sandbox_run 的 Files 用）
- 未启用/不存在 → 可读错误（"技能不存在或未启用"）
- 子 Agent 不可见：技能是主 Agent 的"方法库"，执行经 delegate 派活（与方向 B 一致）

### 5.3 `save_skill` 工具（S1.5，builtin，主 Agent 可见）

- 参数：`{name, display_name?, description, instruction, files?: [{path, content}]}`
- 落库 `source=agent, enabled=false`；返回"已保存为草稿，用户审阅开启后生效"
- 用户在对话里说"做成技能"时，主 Agent 调它；**不可用 save_skill 修改 enabled**（启停只走 Web）

## 6. API 与前端

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/api/v1/skills` | 清单：id/name/display_name/description/enabled/source/文件数（**不含 instruction 正文**） |
| POST | `/api/v1/skills` | 创建 `{name, display_name, description, instruction, files?}`；name 冲突/超限 400 |
| PUT | `/api/v1/skills/:id` | 更新（name 不可变；可含 `enabled`） |
| DELETE | `/api/v1/skills/:id` | 删除（级联删 files） |

前端（SettingsDrawer 新增「技能」tab）：
- 列表：名称/描述/来源徽标（手写/助手生成）/启停开关
- 编辑：textarea（v1，Markdown 纯文本）；文件列表增删（path + content 文本框）
- 来源徽标 `agent` 的未启用技能置顶提示"待审阅"（承接 save_skill 的默认停用）

## 7. 测试计划（TDD）

| # | 层 | 测试 | 断言 |
|---|----|------|------|
| 1 | Service | `TestSkillService_CRUD_Cache` | 增改删后 Catalog() 立即反映；disabled 不进目录 |
| 2 | Service | `TestSkillService_Limits` | name 格式/重复、instruction>32KB、文件数>10、单文件>64KB → 400 类错误 |
| 3 | Service | `TestSaveSkillByAgent` | source=agent、enabled=false、name 冲突不覆盖 |
| 4 | Tool | `TestLoadSkillTool_*` | 加载正文+清单；带 file 返回内容；未启用 404 语义 |
| 5 | Tool | `TestSaveSkillTool_*` | 落库默认停用；缺 description 拒绝 |
| 6 | API | `TestSkillAPI_*` | CRUD 契约、name 不可变（PUT 带 name 变更 → 400）、删除级联 |
| 7 | Prompt | `TestSkillCatalogSection` | 有 enabled 技能时目录段含 name+description；无技能时该段缺席；**目录内容来自 Service 而非常量**（改库不改 prompt 源码即生效） |
| 8 | 回归 | tools/框架工具回归 | load_skill/save_skill 不影响既有工具池;框架工具不 tool 化口径不变 |

## 8. 边界与不做

- Web 富文本/分屏编辑器、导入导出、分享/市场、版本历史：后续按需。
- 子 Agent 技能可见性：技能目录只进主 Agent（执行统一 delegate）。
- 技能级工具白名单/能力裁剪（"该技能只用某些工具"）：v1 靠 instruction 约束，不够再立。
- 技能自动触发（按事件/定时调用技能）：另立项目，与订阅源/loop 体系合流时再议。

## 9. 迭代计划

| 里程碑 | 内容 | 交付物 |
|--------|------|--------|
| S1 | 两表 + SkillService + CRUD API + 前端技能 tab + 目录注入 + `load_skill` | §3~§6 + 测试 1/2/4/6/7/8 |
| S1.5 | `save_skill` Agent 自助生成（默认停用待审） | §5.3 + 测试 3/5 |
| S2 | skill_files 脚本模板 + 沙箱编排写法规范（SKILL.md 指南：何时传脚本进 sandbox_run、Files 对接） | §2 预热 + 17 号联动 |

## 10. 下一步演进方向

- 技能与 loop/订阅联动（定时跑某个技能、产出落到记忆 matter）
- 技能市场/导入导出（JSON 打包）
- instruction 模板化变量（如 `{{bitable_url}}`）

---

**文档版本**：v1.0
**创建日期**：2026-10-11
