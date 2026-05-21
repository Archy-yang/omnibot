# 用户通道关联服务

---

## 文档信息

| 项 | 内容 |
|----|------|
| 适用版本 | v1.3+ |
| 最后更新 | 2026-05-16 |
| 状态 | ✅ 已实现 |

---

## 1. 这个服务解决什么问题？

解决「同一个用户可以在多个平台和机器人对话，记忆和配置打通」的问题。

**之前的问题（v1.2 及之前）：**
- 只有微信一个平台，身份直接是 OpenID
- 用户身份和微信强绑定，无法支持多平台

**现在的方案（v1.3+）：**
- 用户是核心实体，与平台无关
- 一个用户可以关联多个通道身份（微信 OpenID、飞书 UserID、Web SessionID）
- 无论用户从哪个通道进来，都能找到同一个用户，记忆和配置通用

---

## 2. 读之前你需要知道什么？

需要先了解：
- [系统设计总览](../01-高层设计/01-系统设计总览.md) 的分层架构
- [用户服务核心概念](./user-service.md)

---

## 3. 核心领域模型

### UserChannel 实体

| 字段 | 类型 | 说明 |
|------|------|------|
| ID | int64 | 主键 |
| UserID | int64 | 关联的用户 ID |
| ChannelType | string | 通道类型：wechat / feishu / web / ... |
| ChannelUserID | string | 通道内的用户唯一标识（OpenID、UserID 等） |
| ChannelRawData | JSON | 通道特定的额外数据 |
| CreatedAt | time.Time | 创建时间 |
| UpdatedAt | time.Time | 更新时间 |

### 唯一约束

- `(ChannelType, ChannelUserID)` 联合唯一索引
- 同一个用户在同一个通道类型下可以有多个身份（比如同时关注了两个公众号）

---

## 4. 服务接口

### GetOrCreateByChannel

根据通道类型和通道用户 ID 获取或创建用户。

```go
GetOrCreateByChannel(
    channelType string,
    channelUserID string,
) (
    user *domain.User,
    channel *domain.UserChannel,
    isNew bool,
    err error,
)
```

**行为规则：**
1. 先查 `user_channels` 表找是否已存在关联
2. 如果找到，返回对应用户和通道关联
3. 如果是微信且没在新表找到，降级查旧表 `wechat_accounts`
4. 找到旧表数据，自动迁移到新表（写入 user_channels）
5. 都找不到，创建新用户 + 新通道关联
6. 如果是微信，同时写新旧两张表（双写兼容）

### GetByUserID

获取用户的所有通道关联：

```go
GetByUserID(userID int64) ([]*domain.UserChannel, error)
```

### UpdateChannel

更新通道的额外数据：

```go
UpdateChannel(uc *domain.UserChannel) error
```

---

## 5. 数据迁移策略

### 双写期（v1.3 - v1.4）

- 新创建的微信用户，同时写 `wechat_accounts` 和 `user_channels`
- 读取时优先读 `user_channels`，读不到降级读 `wechat_accounts`
- 从旧表读到的数据，自动写入新表

### 过渡期（v1.5）

- 停止写旧表
- 写一个一次性迁移脚本，把所有旧表数据迁移到新表
- 旧表标记为只读

### 清理期（v1.6+）

- 删除 `wechat_accounts` 表和相关代码

---

## 6. 设计决策记录

### 决策1：为什么不用 1:1 关联？
- **背景**：一个用户在一个通道类型下是不是只能有一个身份？
- **决策**：允许 1:N，一个用户可以在同一个通道类型下有多个身份
- **原因**：用户可能同时关注了两个公众号，或者有两个飞书账号

### 决策2：为什么 ChannelRawData 用 JSON 而不是单独字段？
- **背景**：每个通道需要存的额外数据不一样（微信有 UnionID、AppID，飞书有 TenantID）
- **决策**：用 JSON 存，不单独建字段
- **原因**：
  - 新增通道不需要改表结构
  - 各通道的数据差异不会扩散到领域模型
  - 读取时按需解析，不需要的可以忽略

### 决策3：为什么自动迁移而不是手动脚本？
- **背景**：v1.2 的存量数据怎么迁到新表？
- **决策**：访问时自动迁移，按需迁移
- **原因**：
  - 不需要停机迁移
  - 只迁移活跃用户，不活跃的不迁，节省资源
  - 出问题可以随时回滚，影响面小

---

## 7. 下一步演进

- v1.4：增加通道元数据（通道名称、头像、状态）
- v1.5：用户可以在 Web 端管理自己的通道关联
- v2.0：跨通道的用户身份合并（用户可以把自己的微信和飞书账号合并）
