package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"omnibot/internal/middleware"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// journal/matters handler 测试(21 号 J2):契约 + 错误映射 + 归属隔离。

type mockJournal struct {
	items      []memorysvc.JournalItem
	nextCursor string
	journalErr error
	list       []memorysvc.MatterSummary
	listErr    error
	detail     *memorysvc.MatterDetail
	detailErr  error
	updOK      bool
	updErr     error
	updState   *string
	updStatus  *string
}

func (m *mockJournal) Journal(_ context.Context, _ int64, _ string, _, _ *time.Time, _ string, _ int) ([]memorysvc.JournalItem, string, error) {
	return m.items, m.nextCursor, m.journalErr
}
func (m *mockJournal) Matters(int64, string) ([]memorysvc.MatterSummary, error) {
	return m.list, m.listErr
}
func (m *mockJournal) MatterDetail(int64, int64) (*memorysvc.MatterDetail, error) {
	return m.detail, m.detailErr
}
func (m *mockJournal) UpdateMatter(_ int64, _ int64, stateDesc, status *string) (bool, error) {
	m.updState, m.updStatus = stateDesc, status
	return m.updOK, m.updErr
}

func setupJournalRouter(svc JournalService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { // 模拟 AuthRequired(真实中间件同款注入)
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetJournalService(svc)
	r.GET("/api/v1/memories/journal", h.HandleGetJournal)
	r.GET("/api/v1/matters", h.HandleListMatters)
	r.GET("/api/v1/matters/:id", h.HandleGetMatter)
	r.PUT("/api/v1/matters/:id", h.HandleUpdateMatter)
	return r
}

func TestJournalHandler(t *testing.T) {
	mock := &mockJournal{items: []memorysvc.JournalItem{{ID: 1, Content: "条目", Kind: "fact"}}, nextCursor: "CUR"}
	r := setupJournalRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/memories/journal?kind=fact&limit=10", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"next_cursor":"CUR"`)
	assert.Contains(t, w.Body.String(), "条目")

	// from 格式错 → 400
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/memories/journal?from=notatime", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// service 校验错 → 400
	mock2 := &mockJournal{journalErr: memorysvc.ErrJournalInvalidCursor}
	r2 := setupJournalRouter(mock2)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/memories/journal", nil))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestMattersHandler(t *testing.T) {
	mock := &mockJournal{list: []memorysvc.MatterSummary{{ID: 3, Title: "旅行", MemoryCount: 2}}}
	r := setupJournalRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters?status=active", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"memory_count":2`)

	// 详情
	mock.detail = &memorysvc.MatterDetail{ID: 3, Title: "旅行"}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/3", nil))
	require.Equal(t, http.StatusOK, w.Code)

	// 他人档案 → 404
	mock2 := &mockJournal{detailErr: memorysvc.ErrMatterNotFound}
	r2 := setupJournalRouter(mock2)
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/matters/3", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)

	// 纠正:透传指针字段;非法状态 → 400;未命中 → 404;成功 → 200
	mock3 := &mockJournal{updOK: true}
	r3 := setupJournalRouter(mock3)
	body := `{"state_desc":"机票已订","status":"done"}`
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/matters/3", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r3.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, mock3.updState)
	assert.Equal(t, "机票已订", *mock3.updState)

	r4 := setupJournalRouter(&mockJournal{updErr: memorysvc.ErrJournalInvalidStatus})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/matters/3", strings.NewReader(`{"status":"weird"}`))
	req.Header.Set("Content-Type", "application/json")
	r4.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	r5 := setupJournalRouter(&mockJournal{updErr: memorysvc.ErrMatterNotFound})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/matters/3", strings.NewReader(`{"status":"done"}`))
	req.Header.Set("Content-Type", "application/json")
	r5.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
