# 测试计划：internal/client/llm/provider.go

## 测试目的
验证接口定义正确，符合预期使用方式。

## 测试环境
- 静态检查，不需要运行时测试

---

## 检查项
- LLMProvider 接口定义正确，包含 ChatCompletion 方法
- ChatMessage 结构体定义正确，包含 Role 和 Content 字段

**期望断言**：
- 接口方法签名正确，支持 context.Context
- 结构体字段正确导出

---

## 已实现状态
- ✅ 接口定义完成
- ✅ 编译检查通过
