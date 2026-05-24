# 长期记忆 MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the long-term memory MVP so WeChat users can explicitly save, list, clear, and automatically use recent long-term memories in normal conversations.

**Architecture:** Add an entrance-agnostic memory domain, repository, and service, then inject the memory service into the existing chat message context builder. WeChat remains responsible only for command recognition and response formatting; ordinary LLM context construction stays centralized in `MessageService.BuildContextMessages`.

**Tech Stack:** Go, Gin, GORM, SQLite via `github.com/glebarez/sqlite`, PostgreSQL via GORM, Zap logger, testify.

---

## Source Documents

- PRD: `docs/20-产品PRD/in_progress/长期记忆MVP-PRD-v1.3.md`
- Technical design: `docs/superpowers/specs/2026-05-23-long-term-memory-mvp-design.md`
- Architecture rules: `CLAUDE.md`, `docs/10-宪章/开发流程规范.md`, `docs/10-宪章/安全红线.md`, `docs/10-宪章/代码风格规范.md`

## File Structure

Create:

- `internal/domain/memory/memory.go` — pure domain model for one long-term memory row.
- `internal/domain/memory/memory_test.go` — domain constructor and table-name tests.
- `internal/repository/memory/memory_repo.go` — GORM repository for create/list/clear/recent memory operations.
- `internal/repository/memory/memory_repo_test.go` — repository persistence and user-isolation tests.
- `internal/service/memory/memory_service.go` — service-level validation, command-facing memory operations, context recall.
- `internal/service/memory/memory_service_test.go` — service behavior and validation tests.
- `internal/api/wechat/handler_memory_test.go` — WeChat memory command and injection tests.

Modify:

- `internal/db/database.go` — import `internal/domain/memory` and add `&memory.Memory{}` to AutoMigrate.
- `internal/db/database_test.go` — assert `memories` table is created.
- `internal/service/chat/message_service.go` — accept optional memory service and inject memory system message into context.
- `internal/service/chat/message_service_test.go` — preserve existing short-term context tests and add long-term memory injection/degradation tests.
- `internal/api/wechat/handler.go` — add optional memory service, memory command handling, and remove full raw message body logging.
- `internal/api/wechat/handler_test.go` — update mocks if constructor/interface expectations change.
- `internal/api/routes.go` — wire memory repository/service into message service and WeChat handler.

---

### Task 1: Memory Domain and AutoMigration

**Files:**
- Create: `internal/domain/memory/memory.go`
- Create: `internal/domain/memory/memory_test.go`
- Modify: `internal/db/database.go`
- Modify: `internal/db/database_test.go`

- [ ] **Step 1: Write failing domain tests**

Create `internal/domain/memory/memory_test.go`:

```go
package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewMemory(t *testing.T) {
	before := time.Now()

	m := NewMemory(123, "我偏好简洁直接的回答")

	assert.Equal(t, int64(123), m.UserID)
	assert.Equal(t, "我偏好简洁直接的回答", m.Content)
	assert.False(t, m.CreatedAt.Before(before))
	assert.False(t, m.UpdatedAt.Before(before))
}

func TestMemory_TableName(t *testing.T) {
	assert.Equal(t, "memories", Memory{}.TableName())
}
```

- [ ] **Step 2: Run domain tests to verify RED**

Run:

```bash
go test ./internal/domain/memory
```

Expected: FAIL because package or symbols `Memory` and `NewMemory` are not defined.

- [ ] **Step 3: Implement memory domain model**

Create `internal/domain/memory/memory.go`:

```go
package memory

import "time"

type Memory struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"index;not null"`
	Content   string    `gorm:"type:text;not null"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

func (Memory) TableName() string {
	return "memories"
}

func NewMemory(userID int64, content string) *Memory {
	now := time.Now()
	return &Memory{
		UserID:    userID,
		Content:   content,
		CreatedAt: now,
		UpdatedAt: now,
	}
}
```

- [ ] **Step 4: Run domain tests to verify GREEN**

Run:

```bash
go test ./internal/domain/memory
```

Expected: PASS.

- [ ] **Step 5: Write failing AutoMigration test**

Modify `internal/db/database_test.go` by adding this test after `TestAutoMigration_MessagesTable`:

```go
func TestAutoMigration_MemoriesTable(t *testing.T) {
	cfg := &config.DatabaseConfig{
		Driver: "sqlite",
		DSN:    ":memory:",
	}

	db, err := InitDB(cfg)
	require.NoError(t, err)
	require.NotNil(t, db)
	defer db.Close()

	var count int64
	err = db.GetGormDB().Raw("SELECT count(*) FROM sqlite_master WHERE type='table' AND name='memories'").Scan(&count).Error
	require.NoError(t, err)
	assert.Equal(t, int64(1), count)
}
```

- [ ] **Step 6: Run AutoMigration test to verify RED**

Run:

```bash
go test ./internal/db -run TestAutoMigration_MemoriesTable -count=1
```

Expected: FAIL because `memories` table is not created.

- [ ] **Step 7: Register memory model in AutoMigrate**

Modify `internal/db/database.go` imports:

```go
import (
	"context"
	"errors"
	"fmt"
	"time"

	"omnibot/internal/domain/conversation"
	"omnibot/internal/domain/memory"
	"omnibot/internal/domain/user"
	"omnibot/pkg/config"
	zaplogger "omnibot/pkg/logger"

	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)
```

Modify `autoMigrate`:

```go
func autoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&user.User{},
		&user.WechatAccount{},
		&user.UserChannel{},
		&user.LLMConfig{},
		&conversation.Message{},
		&memory.Memory{},
	)
}
```

- [ ] **Step 8: Run migration and domain tests to verify GREEN**

Run:

```bash
go test ./internal/domain/memory ./internal/db -run 'TestNewMemory|TestMemory_TableName|TestAutoMigration_MemoriesTable' -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit domain and migration changes**

Run:

```bash
git add internal/domain/memory/memory.go internal/domain/memory/memory_test.go internal/db/database.go internal/db/database_test.go
git commit -m "feat: add long-term memory domain model"
```

---

### Task 2: Memory Repository

**Files:**
- Create: `internal/repository/memory/memory_repo.go`
- Create: `internal/repository/memory/memory_repo_test.go`

- [ ] **Step 1: Write failing repository tests**

Create `internal/repository/memory/memory_repo_test.go`:

```go
package memory

import (
	"fmt"
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupMemoryRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}))
	return db
}

func TestMemoryRepository_CreateAndListByUserID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "第一条记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "第二条记忆")))

	memories, err := repo.ListByUserID(1)

	require.NoError(t, err)
	require.Len(t, memories, 2)
	assert.Equal(t, "第一条记忆", memories[0].Content)
	assert.Equal(t, "第二条记忆", memories[1].Content)
}

func TestMemoryRepository_IsolatesUsers(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "用户一记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	memories, err := repo.ListByUserID(1)

	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, int64(1), memories[0].UserID)
	assert.Equal(t, "用户一记忆", memories[0].Content)
}

func TestMemoryRepository_DeleteByUserID(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	require.NoError(t, repo.Create(memorydomain.NewMemory(1, "用户一记忆")))
	require.NoError(t, repo.Create(memorydomain.NewMemory(2, "用户二记忆")))

	require.NoError(t, repo.DeleteByUserID(1))

	userOne, err := repo.ListByUserID(1)
	require.NoError(t, err)
	assert.Empty(t, userOne)

	userTwo, err := repo.ListByUserID(2)
	require.NoError(t, err)
	require.Len(t, userTwo, 1)
	assert.Equal(t, "用户二记忆", userTwo[0].Content)
}

func TestMemoryRepository_GetRecentByUserIDReturnsNewestNInAscendingOrder(t *testing.T) {
	db := setupMemoryRepoTestDB(t)
	repo := NewMemoryRepository(db)

	for i := 1; i <= 12; i++ {
		require.NoError(t, repo.Create(memorydomain.NewMemory(1, fmt.Sprintf("记忆 %02d", i))))
	}

	memories, err := repo.GetRecentByUserID(1, 10)

	require.NoError(t, err)
	require.Len(t, memories, 10)
	assert.Equal(t, "记忆 03", memories[0].Content)
	assert.Equal(t, "记忆 12", memories[9].Content)
}
```

- [ ] **Step 2: Run repository tests to verify RED**

Run:

```bash
go test ./internal/repository/memory -count=1
```

Expected: FAIL because `NewMemoryRepository` and repository methods are not defined.

- [ ] **Step 3: Implement memory repository**

Create `internal/repository/memory/memory_repo.go`:

```go
package memory

import (
	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

type MemoryRepository interface {
	Create(memory *memorydomain.Memory) error
	ListByUserID(userID int64) ([]*memorydomain.Memory, error)
	DeleteByUserID(userID int64) error
	GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error)
}

type memoryRepository struct {
	db *gorm.DB
}

func NewMemoryRepository(db *gorm.DB) MemoryRepository {
	return &memoryRepository{db: db}
}

func (r *memoryRepository) Create(memory *memorydomain.Memory) error {
	return r.db.Create(memory).Error
}

func (r *memoryRepository) ListByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

func (r *memoryRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&memorydomain.Memory{}).Error
}

func (r *memoryRepository) GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).
		Find(&memories).Error
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(memories)-1; i < j; i, j = i+1, j-1 {
		memories[i], memories[j] = memories[j], memories[i]
	}

	return memories, nil
}
```

- [ ] **Step 4: Run repository tests to verify GREEN**

Run:

```bash
go test ./internal/repository/memory -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit repository changes**

Run:

```bash
git add internal/repository/memory/memory_repo.go internal/repository/memory/memory_repo_test.go
git commit -m "feat: add long-term memory repository"
```

---

### Task 3: Memory Service

**Files:**
- Create: `internal/service/memory/memory_service.go`
- Create: `internal/service/memory/memory_service_test.go`

- [ ] **Step 1: Write failing service tests**

Create `internal/service/memory/memory_service_test.go`:

```go
package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryRepository struct {
	created      *memorydomain.Memory
	memories     []*memorydomain.Memory
	createErr    error
	listErr      error
	deleteErr    error
	recentErr     error
	deletedUser  int64
	recentLimit  int
	recentUserID int64
}

func (m *mockMemoryRepository) Create(memory *memorydomain.Memory) error {
	m.created = memory
	if m.createErr != nil {
		return m.createErr
	}
	memory.ID = 99
	return nil
}

func (m *mockMemoryRepository) ListByUserID(userID int64) ([]*memorydomain.Memory, error) {
	return m.memories, m.listErr
}

func (m *mockMemoryRepository) DeleteByUserID(userID int64) error {
	m.deletedUser = userID
	return m.deleteErr
}

func (m *mockMemoryRepository) GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error) {
	m.recentUserID = userID
	m.recentLimit = limit
	return m.memories, m.recentErr
}

func TestMemoryService_RememberTrimsAndSavesContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	memory, err := service.Remember(context.Background(), 123, "   我偏好简洁回答   ")

	require.NoError(t, err)
	require.NotNil(t, memory)
	assert.Equal(t, int64(123), repo.created.UserID)
	assert.Equal(t, "我偏好简洁回答", repo.created.Content)
	assert.Equal(t, int64(99), memory.ID)
}

func TestMemoryService_RememberRejectsEmptyContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	memory, err := service.Remember(context.Background(), 123, "   ")

	assert.ErrorIs(t, err, ErrEmptyContent)
	assert.Nil(t, memory)
	assert.Nil(t, repo.created)
}

func TestMemoryService_RememberRejectsTooLongContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)
	content := strings.Repeat("你", MaxMemoryContentLength+1)

	memory, err := service.Remember(context.Background(), 123, content)

	assert.ErrorIs(t, err, ErrContentTooLong)
	assert.Nil(t, memory)
	assert.Nil(t, repo.created)
}

func TestMemoryService_ListReturnsRepositoryMemories(t *testing.T) {
	repo := &mockMemoryRepository{memories: []*memorydomain.Memory{
		memorydomain.NewMemory(123, "第一条"),
	}}
	service := NewMemoryService(repo)

	memories, err := service.List(context.Background(), 123)

	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, "第一条", memories[0].Content)
}

func TestMemoryService_ClearIsIdempotent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	err := service.Clear(context.Background(), 123)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.deletedUser)
}

func TestMemoryService_GetRecentForContextReturnsContents(t *testing.T) {
	repo := &mockMemoryRepository{memories: []*memorydomain.Memory{
		memorydomain.NewMemory(123, "第一条"),
		memorydomain.NewMemory(123, "第二条"),
	}}
	service := NewMemoryService(repo)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.recentUserID)
	assert.Equal(t, 10, repo.recentLimit)
	assert.Equal(t, []string{"第一条", "第二条"}, contents)
}

func TestMemoryService_PropagatesRepositoryErrors(t *testing.T) {
	expectedErr := errors.New("database down")
	repo := &mockMemoryRepository{recentErr: expectedErr}
	service := NewMemoryService(repo)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, contents)
}
```

- [ ] **Step 2: Run service tests to verify RED**

Run:

```bash
go test ./internal/service/memory -count=1
```

Expected: FAIL because `NewMemoryService`, `ErrEmptyContent`, `ErrContentTooLong`, and `MaxMemoryContentLength` are not defined.

- [ ] **Step 3: Implement memory service**

Create `internal/service/memory/memory_service.go`:

```go
package memory

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

const MaxMemoryContentLength = 200

var (
	ErrEmptyContent    = errors.New("memory content is empty")
	ErrContentTooLong = errors.New("memory content is too long")
)

type MemoryService interface {
	Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error)
	List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error)
	Clear(ctx context.Context, userID int64) error
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}

type memoryService struct {
	repo memoryrepo.MemoryRepository
}

func NewMemoryService(repo memoryrepo.MemoryRepository) MemoryService {
	return &memoryService{repo: repo}
}

func (s *memoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, ErrEmptyContent
	}
	if utf8.RuneCountInString(trimmed) > MaxMemoryContentLength {
		return nil, ErrContentTooLong
	}

	memory := memorydomain.NewMemory(userID, trimmed)
	if err := s.repo.Create(memory); err != nil {
		logger.ErrorWithFields("Failed to create memory",
			zap.Int64("user_id", userID),
			zap.Int("content_length", utf8.RuneCountInString(trimmed)),
			zap.String("operation", "memory_create"),
			zap.Error(err),
		)
		return nil, err
	}

	logger.InfoWithFields("Memory created",
		zap.Int64("user_id", userID),
		zap.Int64("memory_id", memory.ID),
		zap.Int("content_length", utf8.RuneCountInString(trimmed)),
		zap.String("operation", "memory_create"),
	)
	return memory, nil
}

func (s *memoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return s.repo.ListByUserID(userID)
}

func (s *memoryService) Clear(ctx context.Context, userID int64) error {
	if err := s.repo.DeleteByUserID(userID); err != nil {
		logger.ErrorWithFields("Failed to clear memories",
			zap.Int64("user_id", userID),
			zap.String("operation", "memory_clear"),
			zap.Error(err),
		)
		return err
	}

	logger.InfoWithFields("Memories cleared",
		zap.Int64("user_id", userID),
		zap.String("operation", "memory_clear"),
	)
	return nil
}

func (s *memoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	memories, err := s.repo.GetRecentByUserID(userID, limit)
	if err != nil {
		return nil, err
	}

	contents := make([]string, 0, len(memories))
	for _, memory := range memories {
		contents = append(contents, memory.Content)
	}
	return contents, nil
}
```

- [ ] **Step 4: Run service tests to verify GREEN**

Run:

```bash
go test ./internal/service/memory -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit service changes**

Run:

```bash
git add internal/service/memory/memory_service.go internal/service/memory/memory_service_test.go
git commit -m "feat: add long-term memory service"
```

---

### Task 4: Inject Long-Term Memory in Message Context

**Files:**
- Modify: `internal/service/chat/message_service.go`
- Modify: `internal/service/chat/message_service_test.go`

- [ ] **Step 1: Add failing MessageService tests**

Modify `internal/service/chat/message_service_test.go` by adding imports if missing:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	chat "omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

Add this mock and tests near the existing `BuildContextMessages` tests:

```go
type mockContextMemoryService struct {
	contents []string
	err      error
	limit    int
	userID   int64
}

func (m *mockContextMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	return nil, nil
}

func (m *mockContextMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return nil, nil
}

func (m *mockContextMemoryService) Clear(ctx context.Context, userID int64) error {
	return nil
}

func (m *mockContextMemoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	m.userID = userID
	m.limit = limit
	return m.contents, m.err
}

func TestMessageService_BuildContextMessages_IncludesLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{"我偏好简洁回答", "我正在开发 OmniBot"}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 2)
	assert.Equal(t, conversation.RoleSystem, ctxMsgs[0].Role)
	assert.Contains(t, ctxMsgs[0].Content, "以下是用户长期记忆")
	assert.Contains(t, ctxMsgs[0].Content, "1. 我偏好简洁回答")
	assert.Contains(t, ctxMsgs[0].Content, "2. 我正在开发 OmniBot")
	assert.Equal(t, conversation.RoleUser, ctxMsgs[1].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[1].Content)
	assert.Equal(t, int64(123), memorySvc.userID)
	assert.Equal(t, MaxContextMemories, memorySvc.limit)
}

func TestMessageService_BuildContextMessages_UsesRecentTenLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{"记忆 03", "记忆 04", "记忆 05", "记忆 06", "记忆 07", "记忆 08", "记忆 09", "记忆 10", "记忆 11", "记忆 12"}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 2)
	assert.Equal(t, conversation.RoleSystem, ctxMsgs[0].Role)
	assert.Contains(t, ctxMsgs[0].Content, "1. 记忆 03")
	assert.Contains(t, ctxMsgs[0].Content, "10. 记忆 12")
	assert.Equal(t, 10, strings.Count(ctxMsgs[0].Content, "记忆 "))
	assert.Equal(t, MaxContextMemories, memorySvc.limit)
}

func TestMessageService_BuildContextMessages_SkipsMemoryMessageWhenNoLongTermMemories(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{contents: []string{}}
	service := NewMessageService(msgRepo, memorySvc)

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 1)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[0].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[0].Content)
}

func TestMessageService_BuildContextMessages_DegradesWhenLongTermMemoryQueryFails(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	memorySvc := &mockContextMemoryService{err: errors.New("memory database down")}
	service := NewMessageService(msgRepo, memorySvc)

	require.NoError(t, msgRepo.Create(conversation.NewUserMessage(123, "历史用户消息", "wx_history")))
	require.NoError(t, msgRepo.Create(conversation.NewAssistantMessage(123, "历史助手回复")))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前用户消息")

	require.NoError(t, err)
	require.Len(t, ctxMsgs, 3)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[0].Role)
	assert.Equal(t, "历史用户消息", ctxMsgs[0].Content)
	assert.Equal(t, conversation.RoleAssistant, ctxMsgs[1].Role)
	assert.Equal(t, "历史助手回复", ctxMsgs[1].Content)
	assert.Equal(t, conversation.RoleUser, ctxMsgs[2].Role)
	assert.Equal(t, "当前用户消息", ctxMsgs[2].Content)
}
```

- [ ] **Step 2: Run MessageService tests to verify RED**

Run:

```bash
go test ./internal/service/chat -run 'TestMessageService_BuildContextMessages' -count=1
```

Expected: FAIL because `NewMessageService` does not accept the memory service and `MaxContextMemories` is not defined.

- [ ] **Step 3: Implement optional memory injection in MessageService**

Modify `internal/service/chat/message_service.go` imports:

```go
import (
	"context"
	"errors"
	"fmt"
	"strings"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	chatrepo "omnibot/internal/repository/chat"
	memorysvc "omnibot/internal/service/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)
```

Modify constants and struct:

```go
const (
	ContextRounds           = 10
	ContextMessagesPerRound = 2
	MaxContextMessages      = ContextRounds * ContextMessagesPerRound
	MaxContextMemories      = 10
)

type messageService struct {
	msgRepo   chatrepo.MessageRepository
	memorySvc memorysvc.MemoryService
}

func NewMessageService(msgRepo chatrepo.MessageRepository, optionalServices ...interface{}) MessageService {
	service := &messageService{msgRepo: msgRepo}
	for _, svc := range optionalServices {
		switch s := svc.(type) {
		case memorysvc.MemoryService:
			service.memorySvc = s
		}
	}
	return service
}
```

Replace `BuildContextMessages` with:

```go
func (s *messageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	memoryMessages := s.buildLongTermMemoryMessages(ctx, userID)

	messages, err := s.msgRepo.GetRecentByUserID(userID, MaxContextMessages)
	if err != nil {
		logger.ErrorWithFields("Failed to get recent messages, degraded to no context",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		messages = nil
	}

	result := make([]llm.ChatMessage, 0, len(memoryMessages)+len(messages)+1)
	result = append(result, memoryMessages...)

	for _, msg := range messages {
		result = append(result, llm.ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	result = append(result, llm.ChatMessage{
		Role:    conversation.RoleUser,
		Content: currentContent,
	})

	return result, nil
}
```

Add helper:

```go
func (s *messageService) buildLongTermMemoryMessages(ctx context.Context, userID int64) []llm.ChatMessage {
	if s.memorySvc == nil || userID <= 0 {
		return nil
	}

	memories, err := s.memorySvc.GetRecentForContext(ctx, userID, MaxContextMemories)
	if err != nil {
		logger.ErrorWithFields("Failed to get long-term memories, degraded to short-term context only",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return nil
	}
	if len(memories) == 0 {
		return nil
	}

	var builder strings.Builder
	builder.WriteString("以下是用户长期记忆，请在回答时自然参考，不要主动提及“我参考了记忆”：\n\n")
	for i, memory := range memories {
		builder.WriteString(fmt.Sprintf("%d. %s", i+1, memory))
		if i < len(memories)-1 {
			builder.WriteString("\n")
		}
	}

	return []llm.ChatMessage{{
		Role:    conversation.RoleSystem,
		Content: builder.String(),
	}}
}
```

- [ ] **Step 4: Run MessageService tests to verify GREEN**

Run:

```bash
go test ./internal/service/chat -run 'TestMessageService_BuildContextMessages' -count=1
```

Expected: PASS, including the pre-existing short-term context tests.

- [ ] **Step 5: Commit MessageService integration**

Run:

```bash
git add internal/service/chat/message_service.go internal/service/chat/message_service_test.go
git commit -m "feat: inject long-term memories into chat context"
```

---

### Task 5: WeChat Memory Commands

**Files:**
- Create: `internal/api/wechat/handler_memory_test.go`
- Modify: `internal/api/wechat/handler.go`

- [ ] **Step 1: Write failing WeChat memory command tests**

Create `internal/api/wechat/handler_memory_test.go`:

```go
package wechat

import (
	"context"
	"errors"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryService struct {
	rememberedUserID  int64
	rememberedContent string
	rememberErr       error
	listMemories      []*memorydomain.Memory
	listErr           error
	clearUserID       int64
	clearErr          error
}

func (m *mockMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	m.rememberedUserID = userID
	m.rememberedContent = content
	if m.rememberErr != nil {
		return nil, m.rememberErr
	}
	memory := memorydomain.NewMemory(userID, content)
	memory.ID = 1
	return memory, nil
}

func (m *mockMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return m.listMemories, m.listErr
}

func (m *mockMemoryService) Clear(ctx context.Context, userID int64) error {
	m.clearUserID = userID
	return m.clearErr
}

func (m *mockMemoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	return nil, nil
}

func TestHandler_HandleMemoryCommand_RememberSuccess(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住 我偏好简洁直接的回答")

	require.True(t, handled)
	assert.Equal(t, int64(42), memoryService.rememberedUserID)
	assert.Equal(t, "我偏好简洁直接的回答", memoryService.rememberedContent)
	assert.Contains(t, reply, "已记住：我偏好简洁直接的回答")
	assert.Contains(t, reply, "请不要保存密码、API Key、身份证号等敏感信息")
}

func TestHandler_HandleMemoryCommand_RememberTrimsLeadingSpaceAfterCommand(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, " #记住    xxx ")

	require.True(t, handled)
	assert.Equal(t, "xxx", memoryService.rememberedContent)
	assert.Contains(t, reply, "已记住：xxx")
}

func TestHandler_HandleMemoryCommand_RememberEmptyContent(t *testing.T) {
	memoryService := &mockMemoryService{rememberErr: memorysvc.ErrEmptyContent}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住")

	require.True(t, handled)
	assert.Contains(t, reply, "请在 #记住 后面输入要长期记住的内容")
	assert.Contains(t, reply, "#记住 我偏好简洁直接的回答")
}

func TestHandler_HandleMemoryCommand_RememberTooLong(t *testing.T) {
	memoryService := &mockMemoryService{rememberErr: memorysvc.ErrContentTooLong}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#记住 "+strings.Repeat("你", memorysvc.MaxMemoryContentLength+1))

	require.True(t, handled)
	assert.Equal(t, "这条记忆太长了，请控制在 200 字以内。", reply)
}

func TestHandler_HandleMemoryCommand_ListMemories(t *testing.T) {
	memoryService := &mockMemoryService{listMemories: []*memorydomain.Memory{
		memorydomain.NewMemory(42, "我偏好简洁直接的回答"),
		memorydomain.NewMemory(42, "我正在开发 OmniBot 项目"),
	}}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Contains(t, reply, "我目前记住了这些信息：")
	assert.Contains(t, reply, "1. 我偏好简洁直接的回答")
	assert.Contains(t, reply, "2. 我正在开发 OmniBot 项目")
}

func TestHandler_HandleMemoryCommand_ListEmpty(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Contains(t, reply, "我还没有长期记住任何信息。")
	assert.Contains(t, reply, "#记住 我偏好简洁直接的回答")
}

func TestHandler_HandleMemoryCommand_Clear(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#清空记忆")

	require.True(t, handled)
	assert.Equal(t, int64(42), memoryService.clearUserID)
	assert.Equal(t, "已清空你的全部长期记忆。", reply)
}

func TestHandler_HandleMemoryCommand_ServiceError(t *testing.T) {
	memoryService := &mockMemoryService{listErr: errors.New("database down")}
	handler := &Handler{memoryService: memoryService}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	require.True(t, handled)
	assert.Equal(t, "服务暂时不可用，请稍后再试", reply)
}

func TestHandler_HandleMemoryCommand_WithoutMemoryServiceDoesNotHandle(t *testing.T) {
	handler := &Handler{}

	reply, handled := handler.handleMemoryCommand(42, "#我的记忆")

	assert.False(t, handled)
	assert.Empty(t, reply)
}
```

- [ ] **Step 2: Run WeChat memory command tests to verify RED**

Run:

```bash
go test ./internal/api/wechat -run 'TestHandler_HandleMemoryCommand' -count=1
```

Expected: FAIL because `Handler.memoryService` and `handleMemoryCommand` are not defined.

- [ ] **Step 3: Add memory service dependency to WeChat handler**

Modify `internal/api/wechat/handler.go` imports:

```go
import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/domain/user"
	chat "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)
```

Modify `Handler`:

```go
type Handler struct {
	config           Config
	llmClient        LLMClient
	userService      UserService
	llmConfigService userService.LLMConfigService
	msgService       chat.MessageService
	memoryService    memoryService.MemoryService
}
```

Modify optional service parsing:

```go
for _, svc := range optionalServices {
	switch s := svc.(type) {
	case userLLMConfigService:
		handler.llmConfigService = s
	case chat.MessageService:
		handler.msgService = s
	case memoryService.MemoryService:
		handler.memoryService = s
	}
}
```

- [ ] **Step 4: Implement memory command handling**

Add this method after `handleConfigCommand` in `internal/api/wechat/handler.go`:

```go
func (h *Handler) handleMemoryCommand(userID int64, content string) (string, bool) {
	if h.memoryService == nil {
		return "", false
	}

	trimmed := strings.TrimSpace(content)
	switch {
	case trimmed == "#记住":
		return h.renderEmptyMemoryContentHint(), true
	case strings.HasPrefix(trimmed, "#记住"):
		memoryContent := strings.TrimSpace(strings.TrimPrefix(trimmed, "#记住"))
		memory, err := h.memoryService.Remember(context.Background(), userID, memoryContent)
		if err != nil {
			switch err {
			case memoryService.ErrEmptyContent:
				return h.renderEmptyMemoryContentHint(), true
			case memoryService.ErrContentTooLong:
				return "这条记忆太长了，请控制在 200 字以内。", true
			default:
				logger.ErrorWithFields("Failed to handle memory remember command",
					zap.Int64("user_id", userID),
					zap.String("operation", "memory_command_remember"),
					zap.Error(err),
				)
				return "服务暂时不可用，请稍后再试", true
			}
		}
		return fmt.Sprintf("已记住：%s\n\n提醒：请不要保存密码、API Key、身份证号等敏感信息。", memory.Content), true
	case trimmed == "#我的记忆":
		memories, err := h.memoryService.List(context.Background(), userID)
		if err != nil {
			logger.ErrorWithFields("Failed to handle memory list command",
				zap.Int64("user_id", userID),
				zap.String("operation", "memory_command_list"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		if len(memories) == 0 {
			return "我还没有长期记住任何信息。\n\n你可以这样告诉我：\n#记住 我偏好简洁直接的回答", true
		}
		var builder strings.Builder
		builder.WriteString("我目前记住了这些信息：\n\n")
		for i, memory := range memories {
			builder.WriteString(fmt.Sprintf("%d. %s", i+1, memory.Content))
			if i < len(memories)-1 {
				builder.WriteString("\n")
			}
		}
		return builder.String(), true
	case trimmed == "#清空记忆":
		if err := h.memoryService.Clear(context.Background(), userID); err != nil {
			logger.ErrorWithFields("Failed to handle memory clear command",
				zap.Int64("user_id", userID),
				zap.String("operation", "memory_command_clear"),
				zap.Error(err),
			)
			return "服务暂时不可用，请稍后再试", true
		}
		return "已清空你的全部长期记忆。", true
	}

	return "", false
}

func (h *Handler) renderEmptyMemoryContentHint() string {
	return "请在 #记住 后面输入要长期记住的内容，例如：\n#记住 我偏好简洁直接的回答"
}
```

- [ ] **Step 5: Route memory commands from text handling**

Modify `handleTextMessage` after config command handling and before ordinary user-message saving:

```go
if h.memoryService != nil {
	if reply, handled := h.handleMemoryCommand(userID, msg.Content); handled {
		if h.msgService != nil && userID > 0 {
			h.msgService.SaveUserMessage(context.Background(), userID, msg.Content, msg.MsgID)
			h.msgService.SaveAssistantMessage(context.Background(), userID, reply)
		}
		return h.buildResponse(msg, reply), nil
	}
}
```

- [ ] **Step 6: Run WeChat memory command tests to verify GREEN**

Run:

```bash
go test ./internal/api/wechat -run 'TestHandler_HandleMemoryCommand' -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit WeChat command changes**

Run:

```bash
git add internal/api/wechat/handler.go internal/api/wechat/handler_memory_test.go
git commit -m "feat: add wechat long-term memory commands"
```

---

### Task 6: Dependency Wiring and WeChat LLM Injection Regression

**Files:**
- Modify: `internal/api/routes.go`
- Modify: `internal/api/wechat/handler_memory_test.go`

- [ ] **Step 1: Write failing constructor and injection test**

Append to `internal/api/wechat/handler_memory_test.go`:

```go
func TestNewHandler_AcceptsMemoryService(t *testing.T) {
	memoryService := &mockMemoryService{}
	handler := NewHandler(Config{Token: "testtoken"}, &MockLLMClient{}, &MockUserService{}, memoryService)

	assert.Same(t, memoryService, handler.memoryService)
}

func TestHandler_HandleTextMessage_MemoryCommandDoesNotCallLLM(t *testing.T) {
	memoryService := &mockMemoryService{}
	llmClient := &MockLLMClient{returnString: "LLM reply"}
	mockUser := &MockUserService{returnUser: &user.User{ID: 42}, returnIsNew: false}
	handler := NewHandler(Config{Token: "testtoken"}, llmClient, mockUser, memoryService)

	msg := &Message{
		ToUserName:   "gh_test",
		FromUserName: "openid_test",
		MsgType:      "text",
		Content:      "#记住 我偏好简洁回答",
		MsgID:        "wx_memory_1",
	}

	response, err := handler.handleTextMessage(msg)

	require.NoError(t, err)
	assert.False(t, llmClient.called)
	assert.Contains(t, response, "已记住：我偏好简洁回答")
}
```

If this file does not already import `omnibot/internal/domain/user`, add it to the import block:

```go
import (
	"context"
	"errors"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/domain/user"
	memorysvc "omnibot/internal/service/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)
```

- [ ] **Step 2: Run constructor and injection tests to verify RED**

Run:

```bash
go test ./internal/api/wechat -run 'TestNewHandler_AcceptsMemoryService|TestHandler_HandleTextMessage_MemoryCommandDoesNotCallLLM' -count=1
```

Expected: FAIL until optional service parsing and `handleTextMessage` memory command routing are complete.

- [ ] **Step 3: Wire memory repository and service in routes**

Modify `internal/api/routes.go` imports:

```go
import (
	"io/fs"
	"net/http"

	"omnibot/frontend"
	"omnibot/internal/api/admin"
	"omnibot/internal/api/web"
	"omnibot/internal/api/wechat"
	channelfactory "omnibot/internal/channel"
	channelweb "omnibot/internal/channel/web"
	"omnibot/internal/client/llm"
	"omnibot/internal/db"
	"omnibot/internal/middleware"
	chatRepo "omnibot/internal/repository/chat"
	memoryRepo "omnibot/internal/repository/memory"
	userRepo "omnibot/internal/repository/user"
	chatService "omnibot/internal/service/chat"
	memoryService "omnibot/internal/service/memory"
	userService "omnibot/internal/service/user"
	"omnibot/pkg/config"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)
```

Modify repository and service wiring:

```go
memoryRepository := memoryRepo.NewMemoryRepository(dbConn.GetGormDB())
```

Place it with other repositories after `userChannelRepository`.

Modify service setup:

```go
memorySvc := memoryService.NewMemoryService(memoryRepository)
msgRepo := chatRepo.NewMessageRepository(dbConn.GetGormDB())
msgSvc := chatService.NewMessageService(msgRepo, memorySvc)
```

Modify WeChat handler construction:

```go
}, llmClient, userSvc, llmConfigSvc, msgSvc, memorySvc)
```

- [ ] **Step 4: Run route package compile test**

Run:

```bash
go test ./internal/api -count=1
```

Expected: PASS.

- [ ] **Step 5: Run WeChat constructor and injection tests to verify GREEN**

Run:

```bash
go test ./internal/api/wechat -run 'TestNewHandler_AcceptsMemoryService|TestHandler_HandleTextMessage_MemoryCommandDoesNotCallLLM' -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit wiring changes**

Run:

```bash
git add internal/api/routes.go internal/api/wechat/handler_memory_test.go
git commit -m "feat: wire long-term memory services"
```

---

### Task 7: Remove Full WeChat Message Body Logging

**Files:**
- Modify: `internal/api/wechat/handler.go`
- Modify: `internal/api/wechat/handler_test.go`

- [ ] **Step 1: Write failing log-safety regression test**

Modify `internal/api/wechat/handler_test.go` by adding this test near existing `HandleMessage` tests:

```go
func TestHandler_HandleMessage_DoesNotRequireRawBodyLogging(t *testing.T) {
	logger.Init(config.LoggerConfig{Level: "info"})

	mockLLM := &MockLLMClient{returnString: "回复"}
	mockUser := &MockUserService{returnUser: user.NewUser(), returnIsNew: false}
	handler := NewHandler(Config{Token: "testtoken"}, mockLLM, mockUser)

	r := gin.New()
	r.POST("/wechat/callback", handler.HandleMessage)

	xmlBody := `<xml>
  <ToUserName><![CDATA[gh_test]]></ToUserName>
  <FromUserName><![CDATA[openid_test]]></FromUserName>
  <CreateTime>1234567890</CreateTime>
  <MsgType><![CDATA[text]]></MsgType>
  <Content><![CDATA[#记住 secret-memory-content]]></Content>
</xml>`

	req := httptest.NewRequest("POST", "/wechat/callback", bytes.NewBufferString(xmlBody))
	req.Header.Set("Content-Type", "application/xml")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockLLM.called)
}
```

This test documents the route still works after removing raw body logging. The actual safety verification is the code change in the next step: `zap.String("body", string(body))` must be removed.

- [ ] **Step 2: Run log-safety regression test before code change**

Run:

```bash
go test ./internal/api/wechat -run TestHandler_HandleMessage_DoesNotRequireRawBodyLogging -count=1
```

Expected: PASS before the implementation change because the behavior still works. This task is a security cleanup with a regression test; the required code review check is that full raw body logging is removed.

- [ ] **Step 3: Remove full raw WeChat body logging**

Modify `internal/api/wechat/handler.go` by replacing:

```go
// 打印原始请求体内容
logger.InfoWithFields("Received raw wechat message", zap.String("body", string(body)))
```

with:

```go
logger.InfoWithFields("Received wechat callback", zap.Int("body_length", len(body)))
```

- [ ] **Step 4: Run WeChat tests to verify behavior remains GREEN**

Run:

```bash
go test ./internal/api/wechat -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit log-safety cleanup**

Run:

```bash
git add internal/api/wechat/handler.go internal/api/wechat/handler_test.go
git commit -m "fix: avoid logging full wechat message bodies"
```

---

### Task 8: Full Backend Verification

**Files:**
- Verify all backend packages.
- Verify changed docs remain present.

- [ ] **Step 1: Run focused memory-related tests**

Run:

```bash
go test ./internal/domain/memory ./internal/repository/memory ./internal/service/memory ./internal/service/chat ./internal/api/wechat ./internal/db -count=1
```

Expected: PASS.

- [ ] **Step 2: Run full backend test suite**

Run:

```bash
make test-backend
```

Expected: PASS.

- [ ] **Step 3: Check git status for intended files only**

Run:

```bash
git status --short
```

Expected changed files are the implementation files listed in this plan plus:

```text
docs/20-产品PRD/in_progress/长期记忆MVP-PRD-v1.3.md
docs/superpowers/specs/2026-05-23-long-term-memory-mvp-design.md
docs/superpowers/plans/2026-05-23-long-term-memory-mvp.md
```

- [ ] **Step 4: Final commit for docs if they were not committed earlier**

Run only if the PRD, spec, or plan documents are still uncommitted:

```bash
git add docs/20-产品PRD/in_progress/长期记忆MVP-PRD-v1.3.md docs/superpowers/specs/2026-05-23-long-term-memory-mvp-design.md docs/superpowers/plans/2026-05-23-long-term-memory-mvp.md
git commit -m "docs: add long-term memory MVP plan"
```

- [ ] **Step 5: Report verification evidence**

Report the exact commands run and their results:

```text
go test ./internal/domain/memory ./internal/repository/memory ./internal/service/memory ./internal/service/chat ./internal/api/wechat ./internal/db -count=1
make test-backend
```

Expected report includes passing exit status for both commands.

---

## Self-Review Checklist

- Spec coverage: domain, repository, service, message context injection, WeChat commands, routes wiring, log safety, and test verification are covered by Tasks 1-8.
- Placeholder scan: this plan contains no unresolved placeholder markers or incomplete sections.
- Type consistency: `MemoryService`, `MemoryRepository`, `MaxMemoryContentLength`, `MaxContextMemories`, and `handleMemoryCommand` names are used consistently across tasks.
