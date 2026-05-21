# 用户体系实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立用户体系基础，实现微信公众号用户自动创建功能，支持后续手机号绑定扩展。

**Architecture:** 采用用户与社交账号分离设计：
- `User` 实体：核心用户，包含手机号、状态等通用字段
- `WechatAccount` 实体：微信账号信息，与 User 多对一关联
- 仓储层负责数据持久化，服务层封装业务逻辑

**Tech Stack:** Go 1.24 + GORM (ORM) + SQLite (测试/开发) + MySQL (生产)

---

## 文件结构映射

| 文件路径 | 职责 | 操作 |
|----------|------|------|
| `internal/domain/user/user.go` | 用户领域模型定义 | 创建 |
| `internal/domain/user/wechat_account.go` | 微信账号领域模型定义 | 创建 |
| `internal/repository/user/user_repo.go` | 用户仓储接口与实现 | 创建 |
| `internal/repository/user/wechat_account_repo.go` | 微信账号仓储接口与实现 | 创建 |
| `internal/service/user/user_service.go` | 用户服务业务逻辑 | 创建 |
| `internal/api/wechat/handler.go` | 集成用户创建逻辑 | 修改 |
| `pkg/config/config.go` | 添加数据库配置 | 修改 |

---

### Task 1: 数据库配置

**Files:**
- Modify: `pkg/config/config.go`

- [ ] **Step 1: 添加数据库配置结构体**

在 `config.go` 的 `Config` 结构体中添加 `Database` 字段：

```go
// Config 应用配置结构体
type Config struct {
    App      AppConfig      `mapstructure:"app"`
    Wechat   WechatConfig   `mapstructure:"wechat"`
    LLM      LLMConfig      `mapstructure:"llm"`
    Memory   MemoryConfig   `mapstructure:"memory"`
    Redis    RedisConfig    `mapstructure:"redis"`
    Logger   LoggerConfig   `mapstructure:"logger"`
    Database DatabaseConfig `mapstructure:"database"`
}
```

在文件末尾添加：

```go
// DatabaseConfig 数据库配置
type DatabaseConfig struct {
    Driver   string `mapstructure:"driver"`   // sqlite, mysql
    DSN      string `mapstructure:"dsn"`      // 连接字符串
    MaxConns int    `mapstructure:"max_conns"`
}
```

- [ ] **Step 2: 验证编译通过**

Run: `go build ./pkg/config/...`
Expected: 编译成功，无错误

- [ ] **Step 3: Commit**

```bash
git add pkg/config/config.go
git commit -m "feat: add database config"
```

---

### Task 2: 用户领域模型

**Files:**
- Create: `internal/domain/user/user.go`
- Test: `internal/domain/user/user_test.go`

- [ ] **Step 1: 编写测试文件**

```go
package user

import (
    "testing"
    "time"
    "github.com/stretchr/testify/assert"
)

func TestNewUser(t *testing.T) {
    user := NewUser()
    
    assert.NotNil(t, user)
    assert.Greater(t, user.ID, int64(0))
    assert.Equal(t, StatusNormal, user.Status)
    assert.False(t, user.PhoneVerified)
    assert.Empty(t, user.Phone)
    assert.WithinDuration(t, time.Now(), user.CreatedAt, time.Second)
    assert.WithinDuration(t, time.Now(), user.UpdatedAt, time.Second)
}

func TestUser_BindPhone(t *testing.T) {
    user := NewUser()
    
    user.BindPhone("13800138000")
    
    assert.Equal(t, "13800138000", user.Phone)
    assert.True(t, user.PhoneVerified)
    assert.NotNil(t, user.PhoneBindTime)
}

func TestUser_StatusFlow(t *testing.T) {
    user := NewUser()
    
    user.Ban()
    assert.Equal(t, StatusBanned, user.Status)
    
    user.Unban()
    assert.Equal(t, StatusNormal, user.Status)
    
    user.SoftDelete()
    assert.Equal(t, StatusDeleted, user.Status)
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test -v ./internal/domain/user -run TestNewUser`
Expected: FAIL with "package not found"

- [ ] **Step 3: 实现用户模型**

```go
package user

import (
    "time"
)

// 用户状态常量
const (
    StatusNormal  = int8(0)
    StatusBanned  = int8(1)
    StatusDeleted = int8(2)
)

// User 核心用户实体
type User struct {
    ID             int64      `gorm:"primaryKey;autoIncrement"`
    Phone          *string    `gorm:"size:20;unique"`
    PhoneVerified  bool       `gorm:"default:false"`
    PhoneBindTime  *time.Time
    Status         int8       `gorm:"default:0;not null"` // 0-正常, 1-封禁, 2-删除
    CreatedAt      time.Time  `gorm:"not null"`
    UpdatedAt      time.Time  `gorm:"not null"`
}

// NewUser 创建新用户
func NewUser() *User {
    now := time.Now()
    return &User{
        Status:    StatusNormal,
        CreatedAt: now,
        UpdatedAt: now,
    }
}

// BindPhone 绑定手机号
func (u *User) BindPhone(phone string) {
    u.Phone = &phone
    u.PhoneVerified = true
    now := time.Now()
    u.PhoneBindTime = &now
    u.UpdatedAt = time.Now()
}

// Ban 封禁用户
func (u *User) Ban() {
    u.Status = StatusBanned
    u.UpdatedAt = time.Now()
}

// Unban 解封用户
func (u *User) Unban() {
    u.Status = StatusNormal
    u.UpdatedAt = time.Now()
}

// SoftDelete 软删除用户
func (u *User) SoftDelete() {
    u.Status = StatusDeleted
    u.UpdatedAt = time.Now()
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test -v ./internal/domain/user -run TestNewUser`
Expected: PASS

Run: `go test -v ./internal/domain/user -run TestUser_BindPhone`
Expected: PASS

Run: `go test -v ./internal/domain/user -run TestUser_StatusFlow`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/user/user.go internal/domain/user/user_test.go
git commit -m "feat: add user domain model"
```

---

### Task 3: 微信账号领域模型

**Files:**
- Create: `internal/domain/user/wechat_account.go`
- Test: `internal/domain/user/wechat_account_test.go`

- [ ] **Step 1: 编写测试文件**

```go
package user

import (
    "testing"
    "time"
    "github.com/stretchr/testify/assert"
)

func TestNewWechatAccount(t *testing.T) {
    user := NewUser()
    account := NewWechatAccount(user.ID, "test_openid_123")
    
    assert.NotNil(t, account)
    assert.Equal(t, user.ID, account.UserID)
    assert.Equal(t, "test_openid_123", account.OpenID)
    assert.Nil(t, account.UnionID)
    assert.WithinDuration(t, time.Now(), account.CreatedAt, time.Second)
}

func TestWechatAccount_SetUnionID(t *testing.T) {
    user := NewUser()
    account := NewWechatAccount(user.ID, "test_openid_123")
    
    unionID := "test_unionid_456"
    account.SetUnionID(unionID)
    
    assert.NotNil(t, account.UnionID)
    assert.Equal(t, unionID, *account.UnionID)
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test -v ./internal/domain/user -run TestNewWechatAccount`
Expected: FAIL

- [ ] **Step 3: 实现微信账号模型**

```go
package user

import (
    "time"
)

// WechatAccount 微信账号实体
// 一个 User 可以关联多个 WechatAccount（不同公众号/小程序）
type WechatAccount struct {
    ID        int64      `gorm:"primaryKey;autoIncrement"`
    UserID    int64      `gorm:"not null;index"`
    OpenID    string     `gorm:"size:128;not null;unique"`
    UnionID   *string    `gorm:"size:128;unique"`
    AppID     string     `gorm:"size:64"` // 来源公众号/小程序 AppID
    CreatedAt time.Time  `gorm:"not null"`
    UpdatedAt time.Time  `gorm:"not null"`
    
    User User `gorm:"foreignKey:UserID"`
}

// NewWechatAccount 创建新微信账号
func NewWechatAccount(userID int64, openID string) *WechatAccount {
    now := time.Now()
    return &WechatAccount{
        UserID:    userID,
        OpenID:    openID,
        CreatedAt: now,
        UpdatedAt: now,
    }
}

// SetUnionID 设置 UnionID
func (w *WechatAccount) SetUnionID(unionID string) {
    w.UnionID = &unionID
    w.UpdatedAt = time.Now()
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test -v ./internal/domain/user -run TestNewWechatAccount`
Expected: PASS

Run: `go test -v ./internal/domain/user -run TestWechatAccount_SetUnionID`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/domain/user/wechat_account.go internal/domain/user/wechat_account_test.go
git commit -m "feat: add wechat account domain model"
```

---

### Task 4: 用户仓储层

**Files:**
- Create: `internal/repository/user/user_repo.go`
- Test: `internal/repository/user/user_repo_test.go`

- [ ] **Step 1: 编写测试文件（SQLite 内存数据库）**

```go
package user

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    "omnibot/internal/domain/user"
)

func setupTestDB(t *testing.T) *gorm.DB {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    assert.NoError(t, err)
    
    err = db.AutoMigrate(&user.User{})
    assert.NoError(t, err)
    
    return db
}

func TestUserRepository_Create(t *testing.T) {
    db := setupTestDB(t)
    repo := NewUserRepository(db)
    
    u := user.NewUser()
    err := repo.Create(u)
    
    assert.NoError(t, err)
    assert.Greater(t, u.ID, int64(0))
}

func TestUserRepository_GetByID(t *testing.T) {
    db := setupTestDB(t)
    repo := NewUserRepository(db)
    
    u := user.NewUser()
    _ = repo.Create(u)
    
    found, err := repo.GetByID(u.ID)
    
    assert.NoError(t, err)
    assert.NotNil(t, found)
    assert.Equal(t, u.ID, found.ID)
}

func TestUserRepository_GetByPhone(t *testing.T) {
    db := setupTestDB(t)
    repo := NewUserRepository(db)
    
    u := user.NewUser()
    u.BindPhone("13800138000")
    _ = repo.Create(u)
    
    found, err := repo.GetByPhone("13800138000")
    
    assert.NoError(t, err)
    assert.NotNil(t, found)
    assert.Equal(t, u.ID, found.ID)
}

func TestUserRepository_Update(t *testing.T) {
    db := setupTestDB(t)
    repo := NewUserRepository(db)
    
    u := user.NewUser()
    _ = repo.Create(u)
    
    u.BindPhone("13800138000")
    err := repo.Update(u)
    
    assert.NoError(t, err)
    
    found, _ := repo.GetByID(u.ID)
    assert.Equal(t, "13800138000", *found.Phone)
    assert.True(t, found.PhoneVerified)
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test -v ./internal/repository/user -run TestUserRepository_Create`
Expected: FAIL

- [ ] **Step 3: 实现用户仓储接口**

```go
package user

import (
    "gorm.io/gorm"
    "omnibot/internal/domain/user"
)

// UserRepository 用户仓储接口
type UserRepository interface {
    Create(u *user.User) error
    GetByID(id int64) (*user.User, error)
    GetByPhone(phone string) (*user.User, error)
    Update(u *user.User) error
}

// GormUserRepository GORM 实现
type GormUserRepository struct {
    db *gorm.DB
}

// NewUserRepository 创建用户仓储
func NewUserRepository(db *gorm.DB) UserRepository {
    return &GormUserRepository{db: db}
}

func (r *GormUserRepository) Create(u *user.User) error {
    return r.db.Create(u).Error
}

func (r *GormUserRepository) GetByID(id int64) (*user.User, error) {
    var u user.User
    err := r.db.First(&u, id).Error
    if err != nil {
        return nil, err
    }
    return &u, nil
}

func (r *GormUserRepository) GetByPhone(phone string) (*user.User, error) {
    var u user.User
    err := r.db.Where("phone = ?", phone).First(&u).Error
    if err != nil {
        return nil, err
    }
    return &u, nil
}

func (r *GormUserRepository) Update(u *user.User) error {
    return r.db.Save(u).Error
}
```

- [ ] **Step 4: 添加 GORM 依赖并运行测试**

Run: `go get gorm.io/gorm && go get gorm.io/driver/sqlite`

Run: `go test -v ./internal/repository/user -run TestUserRepository`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/repository/user/user_repo.go internal/repository/user/user_repo_test.go go.mod go.sum
git commit -m "feat: add user repository"
```

---

### Task 5: 微信账号仓储层

**Files:**
- Create: `internal/repository/user/wechat_account_repo.go`
- Test: `internal/repository/user/wechat_account_repo_test.go`

- [ ] **Step 1: 编写测试文件**

```go
package user

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    domain "omnibot/internal/domain/user"
)

func setupWechatTestDB(t *testing.T) *gorm.DB {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    assert.NoError(t, err)
    
    err = db.AutoMigrate(&domain.User{}, &domain.WechatAccount{})
    assert.NoError(t, err)
    
    return db
}

func TestWechatAccountRepository_Create(t *testing.T) {
    db := setupWechatTestDB(t)
    repo := NewWechatAccountRepository(db)
    
    // 先创建用户
    u := domain.NewUser()
    _ = db.Create(u).Error
    
    account := domain.NewWechatAccount(u.ID, "test_openid_123")
    err := repo.Create(account)
    
    assert.NoError(t, err)
    assert.Greater(t, account.ID, int64(0))
}

func TestWechatAccountRepository_GetByOpenID(t *testing.T) {
    db := setupWechatTestDB(t)
    repo := NewWechatAccountRepository(db)
    
    u := domain.NewUser()
    _ = db.Create(u).Error
    
    account := domain.NewWechatAccount(u.ID, "test_openid_123")
    _ = repo.Create(account)
    
    found, err := repo.GetByOpenID("test_openid_123")
    
    assert.NoError(t, err)
    assert.NotNil(t, found)
    assert.Equal(t, "test_openid_123", found.OpenID)
    assert.Equal(t, u.ID, found.UserID)
}

func TestWechatAccountRepository_GetByUnionID(t *testing.T) {
    db := setupWechatTestDB(t)
    repo := NewWechatAccountRepository(db)
    
    u := domain.NewUser()
    _ = db.Create(u).Error
    
    account := domain.NewWechatAccount(u.ID, "test_openid_123")
    unionID := "test_union_456"
    account.SetUnionID(unionID)
    _ = repo.Create(account)
    
    found, err := repo.GetByUnionID(unionID)
    
    assert.NoError(t, err)
    assert.NotNil(t, found)
    assert.Equal(t, unionID, *found.UnionID)
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test -v ./internal/repository/user -run TestWechatAccountRepository_Create`
Expected: FAIL

- [ ] **Step 3: 实现微信账号仓储**

```go
package user

import (
    "gorm.io/gorm"
    "omnibot/internal/domain/user"
)

// WechatAccountRepository 微信账号仓储接口
type WechatAccountRepository interface {
    Create(account *user.WechatAccount) error
    GetByOpenID(openID string) (*user.WechatAccount, error)
    GetByUnionID(unionID string) (*user.WechatAccount, error)
    Update(account *user.WechatAccount) error
}

// GormWechatAccountRepository GORM 实现
type GormWechatAccountRepository struct {
    db *gorm.DB
}

// NewWechatAccountRepository 创建微信账号仓储
func NewWechatAccountRepository(db *gorm.DB) WechatAccountRepository {
    return &GormWechatAccountRepository{db: db}
}

func (r *GormWechatAccountRepository) Create(account *user.WechatAccount) error {
    return r.db.Create(account).Error
}

func (r *GormWechatAccountRepository) GetByOpenID(openID string) (*user.WechatAccount, error) {
    var account user.WechatAccount
    err := r.db.Where("open_id = ?", openID).First(&account).Error
    if err != nil {
        return nil, err
    }
    return &account, nil
}

func (r *GormWechatAccountRepository) GetByUnionID(unionID string) (*user.WechatAccount, error) {
    var account user.WechatAccount
    err := r.db.Where("union_id = ?", unionID).First(&account).Error
    if err != nil {
        return nil, err
    }
    return &account, nil
}

func (r *GormWechatAccountRepository) Update(account *user.WechatAccount) error {
    return r.db.Save(account).Error
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test -v ./internal/repository/user -run TestWechatAccountRepository`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/repository/user/wechat_account_repo.go internal/repository/user/wechat_account_repo_test.go
git commit -m "feat: add wechat account repository"
```

---

### Task 6: 用户服务层

**Files:**
- Create: `internal/service/user/user_service.go`
- Test: `internal/service/user/user_service_test.go`

- [ ] **Step 1: 编写测试文件**

```go
package user

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
    domain "omnibot/internal/domain/user"
    repo "omnibot/internal/repository/user"
)

func setupServiceTestDB(t *testing.T) *gorm.DB {
    db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
    assert.NoError(t, err)
    
    err = db.AutoMigrate(&domain.User{}, &domain.WechatAccount{})
    assert.NoError(t, err)
    
    return db
}

func TestUserService_GetOrCreateByOpenID_FirstTime(t *testing.T) {
    db := setupServiceTestDB(t)
    userRepo := repo.NewUserRepository(db)
    wechatRepo := repo.NewWechatAccountRepository(db)
    service := NewUserService(userRepo, wechatRepo)
    
    user, isNew, err := service.GetOrCreateByOpenID("new_openid_123")
    
    assert.NoError(t, err)
    assert.True(t, isNew)
    assert.NotNil(t, user)
    assert.Greater(t, user.ID, int64(0))
}

func TestUserService_GetOrCreateByOpenID_Existing(t *testing.T) {
    db := setupServiceTestDB(t)
    userRepo := repo.NewUserRepository(db)
    wechatRepo := repo.NewWechatAccountRepository(db)
    service := NewUserService(userRepo, wechatRepo)
    
    // 第一次创建
    user1, isNew1, _ := service.GetOrCreateByOpenID("existing_openid")
    assert.True(t, isNew1)
    
    // 第二次获取
    user2, isNew2, err := service.GetOrCreateByOpenID("existing_openid")
    
    assert.NoError(t, err)
    assert.False(t, isNew2)
    assert.Equal(t, user1.ID, user2.ID)
}
```

- [ ] **Step 2: 运行测试验证失败**

Run: `go test -v ./internal/service/user -run TestUserService`
Expected: FAIL

- [ ] **Step 3: 实现用户服务**

```go
package user

import (
    "gorm.io/gorm"
    "omnibot/internal/domain/user"
)

// 用户仓储接口
type UserRepository interface {
    Create(u *user.User) error
    GetByID(id int64) (*user.User, error)
    Update(u *user.User) error
}

// 微信账号仓储接口
type WechatAccountRepository interface {
    Create(account *user.WechatAccount) error
    GetByOpenID(openID string) (*user.WechatAccount, error)
    GetByUnionID(unionID string) (*user.WechatAccount, error)
    Update(account *user.WechatAccount) error
}

// UserService 用户服务
type UserService struct {
    userRepo        UserRepository
    wechatRepo      WechatAccountRepository
}

// NewUserService 创建用户服务
func NewUserService(userRepo UserRepository, wechatRepo WechatAccountRepository) *UserService {
    return &UserService{
        userRepo:   userRepo,
        wechatRepo: wechatRepo,
    }
}

// GetOrCreateByOpenID 根据 OpenID 获取或创建用户
// 返回: 用户, 是否新创建, 错误
func (s *UserService) GetOrCreateByOpenID(openID string) (*user.User, bool, error) {
    // 1. 查找微信账号
    account, err := s.wechatRepo.GetByOpenID(openID)
    if err == nil && account != nil {
        // 找到微信账号，获取对应用户
        u, err := s.userRepo.GetByID(account.UserID)
        if err != nil {
            return nil, false, err
        }
        return u, false, nil
    }
    
    // 2. 微信账号不存在，创建新用户和微信账号
    if err != gorm.ErrRecordNotFound {
        return nil, false, err
    }
    
    // 创建用户
    u := user.NewUser()
    if err := s.userRepo.Create(u); err != nil {
        return nil, false, err
    }
    
    // 创建微信账号关联
    account = user.NewWechatAccount(u.ID, openID)
    if err := s.wechatRepo.Create(account); err != nil {
        return nil, false, err
    }
    
    return u, true, nil
}
```

- [ ] **Step 4: 运行测试验证通过**

Run: `go test -v ./internal/service/user -run TestUserService`
Expected: All tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/service/user/user_service.go internal/service/user/user_service_test.go
git commit -m "feat: add user service"
```

---

### Task 7: 集成到微信 Handler

**Files:**
- Modify: `internal/api/wechat/handler.go`
- Test: `internal/api/wechat/handler_test.go`

- [ ] **Step 1: 修改 Handler 集成用户创建逻辑**

在微信消息处理和事件处理中添加用户创建逻辑：

```go
// 在 HandleMessage 开头添加
func (h *Handler) HandleMessage(c *gin.Context) {
    // ... 现有解析代码 ...
    
    // 获取或创建用户
    _, _, err := h.userService.GetOrCreateByOpenID(msg.FromUserName)
    if err != nil {
        // 记录错误，但不影响消息回复
        logger.Error("failed to get or create user: %v", err)
    }
    
    // ... 现有回复代码 ...
}

// 在事件处理中添加 subscribe 事件处理
func (h *Handler) handleEvent(event *WechatMessage) {
    switch event.Event {
    case "subscribe":
        // 用户关注事件 - 创建用户
        _, _, err := h.userService.GetOrCreateByOpenID(event.FromUserName)
        if err != nil {
            logger.Error("failed to create user on subscribe: %v", err)
        }
    case "unsubscribe":
        // 用户取消关注事件 - 暂不处理
    }
}
```

**注意:** 具体代码修改需要根据当前 handler.go 的实际结构进行调整

- [ ] **Step 2: 运行现有测试确保无回归**

Run: `go test -v ./internal/api/wechat/...`
Expected: All tests PASS

- [ ] **Step 3: Commit**

```bash
git add internal/api/wechat/handler.go
git commit -m "feat: integrate user creation into wechat handler"
```

---

## 验收清单

- [ ] 用户与微信账号分离设计，支持后续多登录方式扩展
- [ ] 关注事件自动创建用户
- [ ] 接收消息自动创建用户（兜底）
- [ ] 预留手机号绑定字段
- [ ] 所有单元测试通过
- [ ] 集成测试通过

---

## 后续扩展（二期）

1. 手机号绑定/解绑接口
2. 短信验证码服务
3. 手机号登录接口
4. 用户管理 CRUD 接口
