package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnibot/internal/middleware"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// memory-rules handler 测试(22号 R2):契约 + 错误映射(400/409/404)。

type mockMemoryRules struct {
	list    []*memorydomain.MemoryBlacklist
	added   string
	addRule *memorydomain.MemoryBlacklist
	addDel  int64
	addErr  error
	delOK   bool
}

func (m *mockMemoryRules) ListBlacklist(int64) ([]*memorydomain.MemoryBlacklist, error) {
	return m.list, nil
}
func (m *mockMemoryRules) AddBlacklist(_ int64, pattern string) (*memorydomain.MemoryBlacklist, int64, error) {
	m.added = pattern
	return m.addRule, m.addDel, m.addErr
}
func (m *mockMemoryRules) RemoveBlacklist(int64, int64) (bool, error) { return m.delOK, nil }

func setupMemoryRulesRouter(svc MemoryRuleService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetMemoryRuleService(svc)
	r.GET("/api/v1/memory-rules", h.HandleListMemoryRules)
	r.POST("/api/v1/memory-rules", h.HandleAddMemoryRule)
	r.DELETE("/api/v1/memory-rules/:id", h.HandleDeleteMemoryRule)
	return r
}

func TestMemoryRulesAPI(t *testing.T) {
	mock := &mockMemoryRules{list: []*memorydomain.MemoryBlacklist{{ID: 1, Pattern: "密码"}}}
	r := setupMemoryRulesRouter(mock)

	// list
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/memory-rules", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"pattern":"密码"`)

	// add 成功:回报存量删除条数
	mock2 := &mockMemoryRules{addRule: &memorydomain.MemoryBlacklist{ID: 2, Pattern: "银行卡号"}, addDel: 2}
	r2 := setupMemoryRulesRouter(mock2)
	w = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/memory-rules", strings.NewReader(`{"pattern":"银行卡号"}`))
	req.Header.Set("Content-Type", "application/json")
	r2.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"deleted":2`)

	// 重复 → 409;非法 → 400
	r3 := setupMemoryRulesRouter(&mockMemoryRules{addErr: memoryrepo.ErrBlacklistDuplicate})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/memory-rules", strings.NewReader(`{"pattern":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r3.ServeHTTP(w, req)
	assert.Equal(t, http.StatusConflict, w.Code)

	r4 := setupMemoryRulesRouter(&mockMemoryRules{addErr: memorysvc.ErrBlacklistInvalidPattern})
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/memory-rules", strings.NewReader(`{"pattern":""}`))
	req.Header.Set("Content-Type", "application/json")
	r4.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// delete:200 / 404
	r5 := setupMemoryRulesRouter(&mockMemoryRules{delOK: true})
	w = httptest.NewRecorder()
	r5.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/memory-rules/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	r6 := setupMemoryRulesRouter(&mockMemoryRules{})
	w = httptest.NewRecorder()
	r6.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/memory-rules/1", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
