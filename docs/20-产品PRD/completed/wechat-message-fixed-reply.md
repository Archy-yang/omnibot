# 需求：微信消息接收与固定回复

## 功能概述
当用户在微信公众号发送任意消息时，系统接收消息并返回固定文本回复 **"客服在开发中，敬请期待"**。此功能用于开发阶段占位，确保微信接入流程完整可用。

## 业务流程

```
用户在微信公众号发送消息
  ↓
微信服务器接收用户消息
  ↓
微信服务器向我们的服务发起 POST 请求 (/wechat/callback)
  ↓
我们的服务验证签名、解析消息
  ↓
我们构造 XML 格式的回复消息
  ↓
微信服务器把回复推送给用户
  ↓
用户看到固定回复内容
```

## 功能需求明细

| 序号 | 需求点 | 说明 |
|------|--------|------|
| 1 | 微信接入验证 | 微信服务器首次验证时，发送 GET 请求带 `signature/timestamp/nonce/echostr` 参数，服务验证签名通过后返回 `echostr` |
| 2 | 签名验证失败 | 签名验证不通过时，返回 403 错误 |
| 3 | 消息解析 | 正确解析微信发来的 XML 格式消息，提取必要字段（ToUserName/FromUserName/CreateTime/MsgType/Content） |
| 4 | 固定回复 | 无论用户发送什么内容，都回复固定文本：`客服在开发中，敬请期待` |
| 5 | 回复格式 | 必须使用微信要求的 XML 格式返回，包含正确的标签结构 |

## 输入输出格式

### 微信请求（GET 验证）
```
GET /wechat/callback?signature=xxx×tamp=xxx&nonce=xxx&echostr=xxx
```

### 微信请求（POST 消息）
```xml
<xml>
  <ToUserName><![CDATA[gh_xxx]]></ToUserName>
  <FromUserName><![CDATA[openid_xxx]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[你好]]></Content>
</xml>
```

### 服务响应（文本回复）
```xml
<xml>
  <ToUserName><![CDATA[openid_xxx]]></ToUserName>
  <FromUserName><![CDATA[gh_xxx]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[客服在开发中，敬请期待]]></Content>
</xml>
```

## 非功能需求
- 遵循项目现有的代码风格和目录结构
- 必须提供单元测试
- 使用项目已有的 Gin 框架和配置管理

## 验收标准
- [ ] 微信公众号服务器配置验证能够通过
- [ ] 用户发送任意文本消息，能收到固定回复"客服在开发中，敬请期待"
- [ ] 签名错误时返回 403
- [ ] 单元测试覆盖核心逻辑
