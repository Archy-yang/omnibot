# Web Long-Term Memory Management Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add Web UI and API support for viewing, creating, and clearing long-term memories while preserving existing chat memory injection.

**Architecture:** Reuse the existing backend `memory.MemoryService` and expose it through `internal/api/web.Handler` via `/api/v1/memories`. On the frontend, add a small memory service/store/composable/component and mount it inside the existing settings modal.

**Tech Stack:** Go + Gin + GORM backend; Vue 3 `<script setup lang="ts">` + Pinia + Naive UI + Axios frontend; TDD for backend, frontend type/build/browser verification because no frontend test runner exists.

---

## File Structure

Backend:

- Modify: `internal/api/web/handler.go` — add memory service interface, DTOs, and memory API handlers.
- Create: `internal/api/web/handler_memory_test.go` — focused tests for Web memory API.
- Modify: `internal/api/web/handler_test.go` — update existing `NewHandler` calls and mocks to include memory service.
- Modify: `internal/api/routes.go` — pass `memorySvc` into Web handler and register `/api/v1/memories` routes.
- Modify: `internal/api/web/handler_test.go` or create route-focused test if needed — verify existing chat/config endpoints still compile with the new constructor.

Frontend:

- Modify: `frontend/src/types/api.ts` — add memory API types.
- Create: `frontend/src/services/memory.ts` — wrap `/memories` API calls.
- Create: `frontend/src/stores/memory.ts` — hold memory list and loading states.
- Create: `frontend/src/composables/useMemory.ts` — expose memory store to components.
- Modify: `frontend/src/composables/index.ts` — export `useMemory`.
- Create: `frontend/src/components/functional/MemorySection.vue` — render list, create form, clear confirmation.
- Modify: `frontend/src/components/functional/SettingsPanel.vue` — mount `MemorySection` in the settings modal.

Verification:

- Backend focused: `go test ./internal/api/web -count=1`
- Backend full: `make test-backend`
- Frontend build: `cd frontend && npm run build`
- Browser: start backend/frontend as appropriate and manually verify the Web memory UI golden path.

---

### Task 1: Backend Web Memory API Tests

**Files:**
- Create: `internal/api/web/handler_memory_test.go`
- Modify: `internal/api/web/handler_test.go`

- [ ] **Step 1: Add test imports and memory mock**

Create `internal/api/web/handler_memory_test.go` with this starting content:

```go
package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryService struct {
	memories        []*memorydomain.Memory
	rememberErr     error
	listErr         error
	clearErr        error
	rememberUserID  int64
	rememberContent string
	listUserID      int64
	clearUserID     int64
	clearCalled     bool
}

func (m *mockMemoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	m.rememberUserID = userID
	m.rememberContent = content
	if m.rememberErr != nil {
		return nil, m.rememberErr
	}
	memory := &memorydomain.Memory{
		ID:        99,
		UserID:    userID,
		Content:   strings.TrimSpace(content),
		CreatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
	}
	m.memories = append(m.memories, memory)
	return memory, nil
}

func (m *mockMemoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	m.listUserID = userID
	if m.listErr != nil {
		return nil, m.listErr
	}
	return m.memories, nil
}

func (m *mockMemoryService) Clear(ctx context.Context, userID int64) error {
	m.clearUserID = userID
	m.clearCalled = true
	if m.clearErr != nil {
		return m.clearErr
	}
	m.memories = []*memorydomain.Memory{}
	return nil
}

func newMemoryTestRouter(memorySvc *mockMemoryService) (*gin.Engine, *mockUserService) {
	gin.SetMode(gin.TestMode)
	userSvc := &mockUserService{userID: 42, created: false}
	handler := NewHandler(
		userSvc,
		&mockMessageService{},
		&mockLLMClient{},
		&mockLLMConfigService{},
		memorySvc,
	)

	router := gin.New()
	router.GET("/api/v1/memories", handler.HandleGetMemories)
	router.POST("/api/v1/memories", handler.HandleCreateMemory)
	router.DELETE("/api/v1/memories", handler.HandleClearMemories)
	return router, userSvc
}
```

- [ ] **Step 2: Add failing GET tests**

Append these tests:

```go
func TestHandleGetMemories_WithMemories(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{
		{
			ID:        1,
			UserID:    42,
			Content:   "我偏好简洁直接的回答",
			CreatedAt: time.Date(2026, 5, 24, 10, 0, 0, 0, time.UTC),
		},
	}}
	router, userSvc := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"success":true`)
	assert.Contains(t, w.Body.String(), `"memories"`)
	assert.Contains(t, w.Body.String(), `"id":1`)
	assert.Contains(t, w.Body.String(), "我偏好简洁直接的回答")
	assert.Equal(t, "test-session", userSvc.channelID)
	assert.Equal(t, int64(42), memorySvc.listUserID)
}

func TestHandleGetMemories_Empty(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"memories":[]`)
}

func TestHandleGetMemories_MissingSessionID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "缺少 session_id 参数")
}

func TestHandleGetMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{listErr: errors.New("db down")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodGet, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "db down")
}
```

- [ ] **Step 3: Add failing POST tests**

Append:

```go
func TestHandleCreateMemory_Success(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"session_id":"test-session",
		"content":" 我偏好简洁直接的回答 "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"message":"已记住。"`)
	assert.Contains(t, w.Body.String(), "我偏好简洁直接的回答")
	assert.Equal(t, int64(42), memorySvc.rememberUserID)
	assert.Equal(t, " 我偏好简洁直接的回答 ", memorySvc.rememberContent)
}

func TestHandleCreateMemory_EmptyContent(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: memorysvc.ErrEmptyContent}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"session_id":"test-session",
		"content":"   "
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入要长期记住的内容。")
}

func TestHandleCreateMemory_ContentTooLong(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: memorysvc.ErrContentTooLong}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"session_id":"test-session",
		"content":"too long"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "这条记忆太长了，请控制在 200 字以内。")
}

func TestHandleCreateMemory_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{rememberErr: errors.New("insert failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodPost, "/api/v1/memories", strings.NewReader(`{
		"session_id":"test-session",
		"content":"我偏好简洁回答"
	}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "insert failed")
}
```

- [ ] **Step 4: Add failing DELETE tests**

Append:

```go
func TestHandleClearMemories_Success(t *testing.T) {
	memorySvc := &mockMemoryService{memories: []*memorydomain.Memory{{ID: 1, UserID: 42, Content: "记忆"}}}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "已清空你的全部长期记忆。")
	assert.True(t, memorySvc.clearCalled)
	assert.Equal(t, int64(42), memorySvc.clearUserID)
}

func TestHandleClearMemories_MissingSessionID(t *testing.T) {
	memorySvc := &mockMemoryService{}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "缺少 session_id 参数")
	assert.False(t, memorySvc.clearCalled)
}

func TestHandleClearMemories_ServiceError(t *testing.T) {
	memorySvc := &mockMemoryService{clearErr: errors.New("delete failed")}
	router, _ := newMemoryTestRouter(memorySvc)

	req, _ := http.NewRequest(http.MethodDelete, "/api/v1/memories?session_id=test-session", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "服务暂时不可用，请稍后再试。")
	assert.NotContains(t, w.Body.String(), "delete failed")
}
```

- [ ] **Step 5: Run tests to verify RED**

Run:

```bash
go test ./internal/api/web -run 'TestHandle(Get|Create|Clear)Memories' -count=1
```

Expected: FAIL because `NewHandler` has no memory parameter and `HandleGetMemories` / `HandleCreateMemory` / `HandleClearMemories` are undefined.

- [ ] **Step 6: Commit tests**

Do not commit yet if the repository policy requires green commits only. If using green-only commits, keep these tests unstaged until Task 2 passes.

---

### Task 2: Backend Web Memory API Implementation

**Files:**
- Modify: `internal/api/web/handler.go`
- Modify: `internal/api/web/handler_test.go`

- [ ] **Step 1: Add imports**

In `internal/api/web/handler.go`, add imports:

```go
import (
	"context"
	"errors"
	"net/http"
	"time"

	"omnibot/internal/client/llm"
	memorydomain "omnibot/internal/domain/memory"
	domainuser "omnibot/internal/domain/user"
	memorysvc "omnibot/internal/service/memory"
	userLLM "omnibot/internal/service/user"

	"github.com/gin-gonic/gin"
)
```

- [ ] **Step 2: Add MemoryService interface and handler field**

After `LLMConfigService`, add:

```go
type MemoryService interface {
	Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error)
	List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error)
	Clear(ctx context.Context, userID int64) error
}
```

Update `Handler`:

```go
type Handler struct {
	userService      UserService
	messageService   MessageService
	llmClient        LLMClient
	llmConfigService LLMConfigService
	memoryService    MemoryService
}
```

Update `NewHandler`:

```go
func NewHandler(
	userService UserService,
	messageService MessageService,
	llmClient LLMClient,
	llmConfigService LLMConfigService,
	memoryService MemoryService,
) *Handler {
	return &Handler{
		userService:      userService,
		messageService:   messageService,
		llmClient:        llmClient,
		llmConfigService: llmConfigService,
		memoryService:    memoryService,
	}
}
```

- [ ] **Step 3: Update existing tests for constructor signature**

In `internal/api/web/handler_test.go`, replace every:

```go
handler := NewHandler(userSvc, msgSvc, llmClient, configSvc)
```

with:

```go
handler := NewHandler(userSvc, msgSvc, llmClient, configSvc, &mockMemoryService{})
```

Use replace-all for that exact string.

- [ ] **Step 4: Add DTOs and helper**

Append before the LLM config section in `handler.go`:

```go
type MemoryDTO struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type GetMemoriesRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

type GetMemoriesResponse struct {
	Memories []MemoryDTO `json:"memories"`
}

type CreateMemoryRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

type CreateMemoryResponse struct {
	Message string    `json:"message"`
	Memory  MemoryDTO `json:"memory"`
}

type ClearMemoriesRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

type ClearMemoriesResponse struct {
	Message string `json:"message"`
}

func toMemoryDTO(memory *memorydomain.Memory) MemoryDTO {
	return MemoryDTO{
		ID:        memory.ID,
		Content:   memory.Content,
		CreatedAt: memory.CreatedAt.Format(time.RFC3339),
	}
}
```

- [ ] **Step 5: Implement GET handler**

Add:

```go
func (h *Handler) HandleGetMemories(c *gin.Context) {
	var req GetMemoriesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	memories, err := h.memoryService.List(c.Request.Context(), user.GetID())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	items := make([]MemoryDTO, 0, len(memories))
	for _, memory := range memories {
		items = append(items, toMemoryDTO(memory))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": GetMemoriesResponse{
			Memories: items,
		},
	})
}
```

- [ ] **Step 6: Implement POST handler**

Add:

```go
func (h *Handler) HandleCreateMemory(c *gin.Context) {
	var req CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "请求参数错误",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	memory, err := h.memoryService.Remember(c.Request.Context(), user.GetID(), req.Content)
	if err != nil {
		status := http.StatusInternalServerError
		message := "服务暂时不可用，请稍后再试。"
		if errors.Is(err, memorysvc.ErrEmptyContent) {
			status = http.StatusBadRequest
			message = "请输入要长期记住的内容。"
		}
		if errors.Is(err, memorysvc.ErrContentTooLong) {
			status = http.StatusBadRequest
			message = "这条记忆太长了，请控制在 200 字以内。"
		}
		c.JSON(status, gin.H{
			"success": false,
			"error":   message,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": CreateMemoryResponse{
			Message: "已记住。",
			Memory:  toMemoryDTO(memory),
		},
	})
}
```

- [ ] **Step 7: Implement DELETE handler**

Add:

```go
func (h *Handler) HandleClearMemories(c *gin.Context) {
	var req ClearMemoriesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	if err := h.memoryService.Clear(c.Request.Context(), user.GetID()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": ClearMemoriesResponse{
			Message: "已清空你的全部长期记忆。",
		},
	})
}
```

- [ ] **Step 8: Run focused tests to verify GREEN**

Run:

```bash
go test ./internal/api/web -run 'TestHandle(Get|Create|Clear)Memories' -count=1
```

Expected: PASS.

- [ ] **Step 9: Run full web package tests**

Run:

```bash
go test ./internal/api/web -count=1
```

Expected: PASS.

- [ ] **Step 10: Commit backend handler**

```bash
git add internal/api/web/handler.go internal/api/web/handler_test.go internal/api/web/handler_memory_test.go
git commit -m "feat: add web memory API handlers"
```

---

### Task 3: Backend Route Wiring

**Files:**
- Modify: `internal/api/routes.go`

- [ ] **Step 1: Write failing route registration test**

If there is an existing routes test that can instantiate `SetupRouter`, add route assertions there. If not, add a source-level regression test in `internal/api/routes_test.go`:

```go
package api

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupRouter_RegistersMemoryRoutes(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	content := string(source)

	assert.Contains(t, content, "web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc)")
	assert.Contains(t, content, "r.Group(\"/api/v1/memories\")")
	assert.Contains(t, content, "HandleGetMemories")
	assert.Contains(t, content, "HandleCreateMemory")
	assert.Contains(t, content, "HandleClearMemories")
}
```

- [ ] **Step 2: Run route test to verify RED**

Run:

```bash
go test ./internal/api -run TestSetupRouter_RegistersMemoryRoutes -count=1
```

Expected: FAIL because routes are not registered and `NewHandler` call has old signature.

- [ ] **Step 3: Wire Web handler and routes**

In `internal/api/routes.go`, change:

```go
webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc)
```

to:

```go
webHandler := web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc)
```

Add after the chat API group or before user config group:

```go
memoryAPIGroup := r.Group("/api/v1/memories")
{
	memoryAPIGroup.GET("", webHandler.HandleGetMemories)
	memoryAPIGroup.POST("", webHandler.HandleCreateMemory)
	memoryAPIGroup.DELETE("", webHandler.HandleClearMemories)
}
```

- [ ] **Step 4: Run route test to verify GREEN**

Run:

```bash
go test ./internal/api -run TestSetupRouter_RegistersMemoryRoutes -count=1
```

Expected: PASS.

- [ ] **Step 5: Run backend API tests**

Run:

```bash
go test ./internal/api/... -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit route wiring**

```bash
git add internal/api/routes.go internal/api/routes_test.go
git commit -m "feat: wire web memory routes"
```

---

### Task 4: Frontend Memory API Types and Service

**Files:**
- Modify: `frontend/src/types/api.ts`
- Create: `frontend/src/services/memory.ts`

- [ ] **Step 1: Add API types**

Append to `frontend/src/types/api.ts`:

```ts
export interface MemoryItem {
  id: number;
  content: string;
  created_at: string;
}

export interface GetMemoriesResponse {
  memories: MemoryItem[];
}

export interface CreateMemoryRequest {
  session_id: string;
  content: string;
}

export interface CreateMemoryResponse {
  message: string;
  memory: MemoryItem;
}

export interface ClearMemoriesResponse {
  message: string;
}
```

- [ ] **Step 2: Add memory service**

Create `frontend/src/services/memory.ts`:

```ts
import { request } from '../utils/request';
import type {
  ApiResponse,
  ClearMemoriesResponse,
  CreateMemoryRequest,
  CreateMemoryResponse,
  GetMemoriesResponse,
} from '../types/api';

export const memoryService = {
  async getMemories(sessionId: string): Promise<GetMemoriesResponse> {
    const response = await request.get<ApiResponse<GetMemoriesResponse>>('/memories', {
      params: { session_id: sessionId },
    });
    return response.data.data;
  },

  async createMemory(requestBody: CreateMemoryRequest): Promise<CreateMemoryResponse> {
    const response = await request.post<ApiResponse<CreateMemoryResponse>>('/memories', requestBody);
    return response.data.data;
  },

  async clearMemories(sessionId: string): Promise<ClearMemoriesResponse> {
    const response = await request.delete<ApiResponse<ClearMemoriesResponse>>('/memories', {
      params: { session_id: sessionId },
    });
    return response.data.data;
  },
};

export default memoryService;
```

- [ ] **Step 3: Run frontend build check**

Run:

```bash
cd frontend && npm run build
```

Expected: PASS.

- [ ] **Step 4: Commit frontend service**

```bash
git add frontend/src/types/api.ts frontend/src/services/memory.ts
git commit -m "feat: add frontend memory API service"
```

---

### Task 5: Frontend Memory Store and Composable

**Files:**
- Create: `frontend/src/stores/memory.ts`
- Create: `frontend/src/composables/useMemory.ts`
- Modify: `frontend/src/composables/index.ts`

- [ ] **Step 1: Add memory store**

Create `frontend/src/stores/memory.ts`:

```ts
import { defineStore } from 'pinia';
import { ref } from 'vue';
import { memoryService } from '../services/memory';
import { useSession } from '../composables/useSession';
import type { CreateMemoryResponse, ClearMemoriesResponse, MemoryItem } from '../types/api';

export const useMemoryStore = defineStore('memory', () => {
  const { sessionId } = useSession();

  const memories = ref<MemoryItem[]>([]);
  const isLoading = ref(false);
  const isCreating = ref(false);
  const isClearing = ref(false);

  const loadMemories = async (): Promise<void> => {
    isLoading.value = true;
    try {
      const response = await memoryService.getMemories(sessionId.value);
      memories.value = response.memories;
    } finally {
      isLoading.value = false;
    }
  };

  const createMemory = async (content: string): Promise<CreateMemoryResponse> => {
    isCreating.value = true;
    try {
      const response = await memoryService.createMemory({
        session_id: sessionId.value,
        content,
      });
      memories.value = [...memories.value, response.memory];
      return response;
    } finally {
      isCreating.value = false;
    }
  };

  const clearMemories = async (): Promise<ClearMemoriesResponse> => {
    isClearing.value = true;
    try {
      const response = await memoryService.clearMemories(sessionId.value);
      memories.value = [];
      return response;
    } finally {
      isClearing.value = false;
    }
  };

  return {
    memories,
    isLoading,
    isCreating,
    isClearing,
    loadMemories,
    createMemory,
    clearMemories,
  };
});
```

- [ ] **Step 2: Add composable**

Create `frontend/src/composables/useMemory.ts`:

```ts
import { computed } from 'vue';
import { useMemoryStore } from '../stores/memory';

export function useMemory() {
  const memoryStore = useMemoryStore();

  const memories = computed(() => memoryStore.memories);
  const isLoading = computed(() => memoryStore.isLoading);
  const isCreating = computed(() => memoryStore.isCreating);
  const isClearing = computed(() => memoryStore.isClearing);

  return {
    memories,
    isLoading,
    isCreating,
    isClearing,
    loadMemories: memoryStore.loadMemories,
    createMemory: memoryStore.createMemory,
    clearMemories: memoryStore.clearMemories,
  };
}
```

- [ ] **Step 3: Export composable**

Modify `frontend/src/composables/index.ts`:

```ts
/**
 * Composables 统一导出
 */
export { useChat } from './useChat';
export { useSession } from './useSession';
export { useToast } from './useToast';
export { useSettings } from './useSettings';
export { useMemory } from './useMemory';
```

- [ ] **Step 4: Run frontend build check**

Run:

```bash
cd frontend && npm run build
```

Expected: PASS.

- [ ] **Step 5: Commit store/composable**

```bash
git add frontend/src/stores/memory.ts frontend/src/composables/useMemory.ts frontend/src/composables/index.ts
git commit -m "feat: add frontend memory state"
```

---

### Task 6: Frontend Memory UI Component

**Files:**
- Create: `frontend/src/components/functional/MemorySection.vue`

- [ ] **Step 1: Create component**

Create `frontend/src/components/functional/MemorySection.vue`:

```vue
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue';
import { useMemory } from '@/composables/useMemory';
import { useToast } from '@/composables/useToast';

const { memories, isLoading, isCreating, isClearing, loadMemories, createMemory, clearMemories } = useMemory();
const { success, error } = useToast();

const memoryInput = ref('');

const trimmedMemory = computed(() => memoryInput.value.trim());
const memoryLength = computed(() => [...trimmedMemory.value].length);
const hasMemories = computed(() => memories.value.length > 0);

const getErrorMessage = (err: unknown): string => {
  return err instanceof Error ? err.message : '服务暂时不可用，请稍后再试。';
};

const handleCreateMemory = async (): Promise<void> => {
  if (!trimmedMemory.value) {
    error('请输入要长期记住的内容。');
    return;
  }

  if (memoryLength.value > 200) {
    error('这条记忆太长了，请控制在 200 字以内。');
    return;
  }

  try {
    await createMemory(trimmedMemory.value);
    memoryInput.value = '';
    success('已记住。提醒：请不要保存密码、API Key、身份证号等敏感信息。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

const handleClearMemories = async (): Promise<void> => {
  try {
    await clearMemories();
    success('已清空你的全部长期记忆。');
  } catch (err) {
    error(getErrorMessage(err));
  }
};

onMounted(() => {
  loadMemories().catch((err: unknown) => {
    error(getErrorMessage(err));
  });
});
</script>

<template>
  <div class="space-y-4">
    <NAlert type="warning" title="安全提醒">
      请不要保存密码、API Key、身份证号等敏感信息。
    </NAlert>

    <div class="space-y-2">
      <NInput
        v-model:value="memoryInput"
        type="textarea"
        placeholder="输入希望助手长期记住的偏好、背景或项目说明"
        :autosize="{ minRows: 2, maxRows: 4 }"
        :maxlength="220"
      />
      <div class="flex items-center justify-between text-xs text-gray-500">
        <span>{{ memoryLength }}/200</span>
        <NButton
          type="primary"
          size="small"
          :loading="isCreating"
          :disabled="!trimmedMemory || memoryLength > 200"
          @click="handleCreateMemory"
        >
          保存记忆
        </NButton>
      </div>
    </div>

    <NSpin :show="isLoading">
      <NEmpty v-if="!hasMemories" description="我还没有长期记住任何信息。">
        <template #extra>
          <span class="text-sm text-gray-500">
            你可以添加一条希望助手长期记住的偏好、背景或项目说明。
          </span>
        </template>
      </NEmpty>

      <NList v-else bordered>
        <NListItem v-for="(memory, index) in memories" :key="memory.id">
          <div class="flex gap-2 text-sm leading-6">
            <span class="text-gray-400">{{ index + 1 }}.</span>
            <span class="whitespace-pre-wrap break-words">{{ memory.content }}</span>
          </div>
        </NListItem>
      </NList>
    </NSpin>

    <div class="flex justify-end">
      <NPopconfirm
        positive-text="确认清空"
        negative-text="取消"
        @positive-click="handleClearMemories"
      >
        <template #trigger>
          <NButton type="error" size="small" :loading="isClearing" :disabled="!hasMemories">
            清空全部长期记忆
          </NButton>
        </template>
        确定要清空全部长期记忆吗？清空后无法恢复。
      </NPopconfirm>
    </div>
  </div>
</template>
```

- [ ] **Step 2: Run frontend build check**

Run:

```bash
cd frontend && npm run build
```

Expected: PASS. If Naive UI auto-import does not include a component used above, add the missing import according to existing project setup rather than introducing a new dependency.

- [ ] **Step 3: Commit component**

```bash
git add frontend/src/components/functional/MemorySection.vue
git commit -m "feat: add memory settings section"
```

---

### Task 7: Settings Panel Integration

**Files:**
- Modify: `frontend/src/components/functional/SettingsPanel.vue`

- [ ] **Step 1: Import MemorySection**

At the top of `SettingsPanel.vue`, add:

```ts
import MemorySection from '@/components/functional/MemorySection.vue';
```

- [ ] **Step 2: Mount MemorySection**

After the LLM config form section and before `</NForm>`, add:

```vue
<NDivider title-placement="left">长期记忆</NDivider>
<MemorySection />
```

Keep the existing LLM config controls unchanged.

- [ ] **Step 3: Run frontend build check**

Run:

```bash
cd frontend && npm run build
```

Expected: PASS.

- [ ] **Step 4: Commit integration**

```bash
git add frontend/src/components/functional/SettingsPanel.vue
git commit -m "feat: show memory management in settings"
```

---

### Task 8: Full Verification and Browser Acceptance

**Files:**
- No code files unless verification exposes a bug.

- [ ] **Step 1: Run backend tests**

Run:

```bash
make test-backend
```

Expected: PASS.

- [ ] **Step 2: Run frontend build**

Run:

```bash
cd frontend && npm run build
```

Expected: PASS.

- [ ] **Step 3: Run full project build**

Run:

```bash
make build
```

Expected: PASS.

- [ ] **Step 4: Start the app for browser verification**

Use the project’s normal local run path. If you need frontend-only verification, run:

```bash
make dev
```

If you need backend API verification, run backend in a separate session using the existing project command:

```bash
go run cmd/server/main.go
```

- [ ] **Step 5: Browser golden path**

Open the Web chat UI and verify:

1. Settings opens successfully.
2. `长期记忆` section is visible.
3. Empty state appears when no memories exist.
4. Adding `我偏好简洁直接的回答` succeeds.
5. The new memory appears in the list.
6. Empty input shows `请输入要长期记住的内容。` and does not call save.
7. More than 200 Unicode characters shows `这条记忆太长了，请控制在 200 字以内。`.
8. Clear memory prompts confirmation.
9. Confirmed clear empties the list.
10. Sending a normal chat message still returns an assistant response.

- [ ] **Step 6: Security spot check**

Search the new code for unsafe content logging:

```bash
grep -R "memory.content\|Content:" internal/api/web frontend/src/services/memory.ts frontend/src/stores/memory.ts frontend/src/components/functional/MemorySection.vue
```

Expected: frontend display/type code may reference content, but backend logs should not include full memory content.

- [ ] **Step 7: Commit verification fixes if needed**

Only if verification required fixes:

```bash
git add <fixed-files>
git commit -m "fix: stabilize web memory management"
```

---

## Final Review Checklist

- [ ] Backend API layer depends only on service interfaces.
- [ ] No handler directly imports repository or DB.
- [ ] Web memory routes use current `session_id` to resolve user.
- [ ] Memory list/create/clear are isolated by `UserID`.
- [ ] Empty and too-long memory errors use PRD text.
- [ ] Internal errors return `服务暂时不可用，请稍后再试。`.
- [ ] Full memory content is not logged.
- [ ] Frontend uses `<script setup lang="ts">`.
- [ ] Frontend adds no `any`.
- [ ] Frontend does not store memories in localStorage.
- [ ] Frontend does not use `v-html` for memory content.
- [ ] Existing Web chat and LLM config still work.
- [ ] WeChat memory command files are untouched.
