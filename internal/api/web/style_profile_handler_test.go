package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"omnibot/internal/middleware"
	memorydomain "omnibot/internal/domain/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// style-profile handler 测试(24号 M1):契约 + 归属隔离(404)。

type mockStyleProfile struct {
	view  *memorysvc.StyleProfileView
	delOK bool
}

func (m *mockStyleProfile) StyleProfile(int64, int) (*memorysvc.StyleProfileView, error) {
	return m.view, nil
}
func (m *mockStyleProfile) DeleteStyleObservation(int64, int64) (bool, error) { return m.delOK, nil }
func (m *mockStyleProfile) DeleteCognitionChange(int64, int64) (bool, error)  { return m.delOK, nil }

func setupStyleProfileRouter(svc StyleProfileService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(middleware.AuthUserIDKey, int64(42))
		c.Next()
	})
	h := &Handler{}
	h.SetStyleProfileService(svc)
	r.GET("/api/v1/style-profile", h.HandleGetStyleProfile)
	r.DELETE("/api/v1/style-profile/observation/:id", h.HandleDeleteStyleObservation)
	r.DELETE("/api/v1/style-profile/change/:id", h.HandleDeleteCognitionChange)
	return r
}

func TestStyleProfileAPI(t *testing.T) {
	mock := &mockStyleProfile{view: &memorysvc.StyleProfileView{
		Observations: []memorysvc.StyleObservationView{{
			ID: 1, Dimension: memorydomain.DimensionTone, Content: "直接", CreatedAt: time.Now(),
		}},
		Changes: []memorysvc.CognitionChangeView{{ID: 2, Content: "从 X 改为 Y", CreatedAt: time.Now()}},
	}}
	r := setupStyleProfileRouter(mock)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/style-profile", nil))
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"dimension":"tone"`)
	assert.Contains(t, w.Body.String(), "从 X 改为 Y")

	// 删除命中 200 / 未命中 404
	r2 := setupStyleProfileRouter(&mockStyleProfile{delOK: true})
	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/observation/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	w = httptest.NewRecorder()
	r2.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/change/1", nil))
	assert.Equal(t, http.StatusOK, w.Code)

	r3 := setupStyleProfileRouter(&mockStyleProfile{})
	w = httptest.NewRecorder()
	r3.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/v1/style-profile/observation/1", nil))
	assert.Equal(t, http.StatusNotFound, w.Code)
}
