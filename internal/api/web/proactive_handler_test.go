package web

// proactive handler 测试(26 号 T6):settings 契约 + hour 校验 + 记录列表。

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	butlerdomain "omnibot/internal/domain/butler"
	"omnibot/internal/middleware"
	butlersvc "omnibot/internal/service/butler"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockProactive struct {
	enabled     bool
	hour        int
	updateErr   error
	updatedEn   bool
	updatedHour int
	notes       []*butlerdomain.ProactiveNote
}

func (m *mockProactive) GetSettings(int64) (bool, int, error) {
	return m.enabled, m.hour, nil
}
func (m *mockProactive) UpdateSettings(_ int64, enabled bool, hour int) error {
	m.updatedEn, m.updatedHour = enabled, hour
	return m.updateErr
}
func (m *mockProactive) ListNotes(int64, int) ([]*butlerdomain.ProactiveNote, error) {
	return m.notes, nil
}
func (m *mockProactive) SetNoteFeedback(int64, int64, string) error { return nil }

func setupProactiveRouter(svc ProactiveSettingsAPIService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetProactiveService(svc)
	r.GET("/api/v1/proactive-settings", h.HandleGetProactiveSettings)
	r.PUT("/api/v1/proactive-settings", h.HandleUpdateProactiveSettings)
	r.GET("/api/v1/proactive-notes", h.HandleListProactiveNotes)
	return r
}

func TestProactiveSettingsAPI_Get(t *testing.T) {
	mock := &mockProactive{enabled: false, hour: 9}
	r := setupProactiveRouter(mock)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proactive-settings", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"enabled":false`)
	assert.Contains(t, w.Body.String(), `"hour":9`)
}

func TestProactiveSettingsAPI_Update(t *testing.T) {
	mock := &mockProactive{}
	r := setupProactiveRouter(mock)

	// 开启 + 传时刻
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/proactive-settings",
		strings.NewReader(`{"enabled": true, "hour": 10}`))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mock.updatedEn)
	assert.Equal(t, 10, mock.updatedHour)

	// 只开关不传 hour:保留现值
	mock2 := &mockProactive{hour: 11}
	r2 := setupProactiveRouter(mock2)
	w2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/proactive-settings",
		strings.NewReader(`{"enabled": false}`))
	r2.ServeHTTP(w2, req2)
	require.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, 11, mock2.updatedHour, "未传 hour 保留现值")
}

func TestProactiveSettingsAPI_UpdateInvalidHour(t *testing.T) {
	mock := &mockProactive{updateErr: errors.New("hour 超出范围(0-23): 24")}
	r := setupProactiveRouter(mock)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/proactive-settings",
		strings.NewReader(`{"enabled": true, "hour": 24}`))
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestProactiveNotesAPI_List(t *testing.T) {
	mock := &mockProactive{}
	r := setupProactiveRouter(mock)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proactive-notes?limit=20", nil)
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"notes":`)
}

func TestProactiveAPI_ServiceNotWired(t *testing.T) {
	r := setupProactiveRouter(nil)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/proactive-settings", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

var _ = time.Now // 保留 time 引用(未来断言扩展)

// 29号 N3:送达反馈 PUT /proactive-notes/:id/feedback——三态契约 + 400/404。
type feedbackMock struct {
	mockProactive
	fbID int64
	fb   string
	err  error
}

func (m *feedbackMock) SetNoteFeedback(_ int64, id int64, feedback string) error {
	m.fbID, m.fb = id, feedback
	if m.err != nil {
		return m.err
	}
	// 模拟 service 校验语义:非法值 → ErrNoteFeedbackInvalid
	switch feedback {
	case "positive", "negative", "clear", "":
		return nil
	default:
		return butlersvc.ErrNoteFeedbackInvalid
	}
}

func TestProactiveNotesAPI_SetFeedback(t *testing.T) {
	svc := &feedbackMock{}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetProactiveService(svc)
	r.PUT("/api/v1/proactive-notes/:id/feedback", h.HandleSetNoteFeedback)

	// negative
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/proactive-notes/7/feedback",
		strings.NewReader(`{"feedback": "negative"}`))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, int64(7), svc.fbID)
	assert.Equal(t, "negative", svc.fb)

	// clear(撤销)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/proactive-notes/7/feedback",
		strings.NewReader(`{"feedback": "clear"}`))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "clear", svc.fb)

	// 非法值 → 400
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/proactive-notes/7/feedback",
		strings.NewReader(`{"feedback": "meh"}`))
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)

	// 未命中 → 404
	svc2 := &feedbackMock{err: butlersvc.ErrNoteNotFound}
	r2 := gin.New()
	r2.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h2 := &Handler{}
	h2.SetProactiveService(svc2)
	r2.PUT("/api/v1/proactive-notes/:id/feedback", h2.HandleSetNoteFeedback)
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/v1/proactive-notes/99/feedback",
		strings.NewReader(`{"feedback": "positive"}`))
	r2.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}
