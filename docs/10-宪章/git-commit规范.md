# Git 提交规范

本文档定义本项目 Git 提交信息的格式和规范。

---

## 提交信息格式

```
<type>(<scope>): <subject>
```

### type 类型

| 类型 | 说明 |
|------|------|
| feat | 新功能 |
| fix | 修复 bug |
| docs | 文档变更 |
| style | 代码格式调整（不影响代码运行） |
| refactor | 重构（不新增功能，不修 bug） |
| test | 测试相关 |
| chore | 构建/工具相关 |
| perf | 性能优化 |
| revert | 回滚提交 |

### scope
- 本次提交影响的范围
- 通常是模块名：llm-config, wechat, message, db 等
- 如果影响多个模块可以用 *

### subject
- 简短描述本次提交的内容
- 使用祈使句，现在时态
- 首字母小写
- 结尾不加句号

---

## 示例

```
feat(llm-config): add AES encryption for API key storage
fix(wechat): handle empty message content gracefully
docs(changelog): update v1.1.0 release notes
refactor(message): extract MessageService interface
test(user): add unit tests for LLMConfigService
chore(deps): upgrade gin to v1.9.0
```

---

## 提交原则

- 每个提交只做一件事
- 提交粒度适中，不要一次提交太多内容
- 提交信息必须清晰表达本次提交的目的
- 不提交临时文件、注释掉的代码、本地配置

---

**文档版本**：v2.0
**创建日期**：2026-05-16
