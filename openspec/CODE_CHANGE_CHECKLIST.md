# 代码变更规范同步检查清单

每次提交代码变更前，请对照以下清单检查是否需要同步更新规范文档。

---

## 🔍 变更前检查

### [ ] 1. 是否修改了对外 API？
- [ ] 新增或修改 HTTP 端点路径
- [ ] 修改请求参数格式或验证规则
- [ ] 修改响应体结构或字段
- [ ] 修改错误码或错误消息格式

**需同步更新**: `openspec/specs/<capability>/spec.md`

---

### [ ] 2. 是否修改了数据结构？
- [ ] 新增或修改配置结构体字段
- [ ] 修改数据库表结构或 DTO
- [ ] 修改消息格式或协议

**需同步更新**: 对应能力域 spec.md 中的数据结构描述

---

### [ ] 3. 是否修改了业务流程？
- [ ] 修改了核心业务逻辑流程
- [ ] 新增或修改了状态转移规则
- [ ] 修改了错误处理或降级逻辑

**需同步更新**: 对应能力域 spec.md 中的 Requirement 描述和 Scenarios

---

### [ ] 4. 是否新增了能力？
- [ ] 新增了完整的功能模块
- [ ] 新增了独立的 API 组
- [ ] 新增了外部系统集成

**需同步更新**:
- 创建新的能力域目录 `openspec/specs/<new-capability>/spec.md`
- 更新 `docs/README.md` 中的能力域列表

---

### [ ] 5. 是否修改了非功能特性？
- [ ] 修改性能指标或超时配置
- [ ] 修改安全策略或认证方式
- [ ] 修改日志格式或级别

**需同步更新**: 对应规范文档中的非功能需求部分

---

## ✅ 提交前最终检查

| 检查项 | 状态 |
|--------|------|
| 规范文档与代码实现保持一致 | ▢ |
| 所有新功能都有对应的验收场景（Scenario） | ▢ |
| 所有修改的 API 端点都在规范中更新 | ▢ |
| README 中的文档索引已同步 | ▢ |

---

## 📋 快速参考：代码目录 → 规范文档映射

| 代码路径 | 规范文档 |
|----------|----------|
| `internal/api/wechat/` | `openspec/specs/wechat-callback-api/spec.md` |
| `internal/client/llm/` | `openspec/specs/llm-client-integration/spec.md` |
| `internal/api/admin/` | `openspec/specs/admin-api/spec.md` |
| `pkg/config/` | `openspec/specs/configuration-system/spec.md` |
| `pkg/logger/` | `openspec/specs/logging-system/spec.md` |

---

## 💡 提示

- **先更新规范，再写代码**：遵循规范驱动开发（Spec-Driven Development）流程
- **小步迭代**：每次变更只影响一个或少数能力域，降低同步成本
- **PR 描述模板**：在 PR 描述中添加"规范文档同步"检查项
