# Git 提交规范

## Commit Message 格式

```
<type>(<scope>): <subject>

<body>

<footer>
```

---

## Type 类型

| 类型 | 说明 |
|------|------|
| feat | 新功能 |
| fix | 修复 bug |
| docs | 文档更新 |
| style | 代码格式（不影响代码运行） |
| refactor | 重构（既不是新增功能，也不是修改 bug） |
| perf | 性能优化 |
| test | 测试相关 |
| chore | 构建过程或辅助工具的变动 |
| revert | 回滚提交 |
| ci | CI/CD 相关 |

---

## Scope（可选）

影响的模块，如：`chat`, `settings`, `router`, `build`, `deps`

## Subject

简短描述，不超过 50 字符，中文或英文均可。

使用动词开头：
- add: 添加新功能/新文件
- remove: 删除功能/文件
- update: 更新功能/文档
- fix: 修复 bug
- refactor: 重构代码
- optimize: 性能优化

## Body（可选）

详细描述，可以多行：
- 为什么做这个变更？
- 主要改动点是什么？
- 有什么需要注意的？

## Footer（可选）

- 关联 Issue：`Closes #123`
- 破坏性变更：`BREAKING CHANGE: 描述`

---

## 示例

```
feat(chat): 支持 Markdown 消息渲染

- 集成 markdown-it 解析器
- 支持代码高亮
- 自动识别链接

Closes #42
```

```
fix(chat): 修复消息重复发送的 bug

原因：点击发送按钮后没有立即禁用，导致快速点击多次。
```
