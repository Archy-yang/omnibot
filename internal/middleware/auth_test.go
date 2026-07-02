package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/auth"
)

const authMwTestSecret = "test-secret-32-chars-min-len-ok!"

func init() {
	gin.SetMode(gin.TestMode)
}

// 构造一个挂 AuthRequired + 一个探测 handler 的路由,handler 把 user_id 回写到 header 方便断言
func newAuthTestRouter(jwtSvc *auth.JWTService) *gin.Engine {
	r := gin.New()
	g := r.Group("/api")
	g.Use(AuthRequired(jwtSvc))
	g.GET("/whoami", func(c *gin.Context) {
		uid, exists := c.Get(AuthUserIDKey)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "user_id missing"})
			return
		}
		id, ok := uid.(int64)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "user_id not int64"})
			return
		}
		c.Header("X-User-ID", string(rune(id)+'0')) // 简单序列化,int64 → 单字符,uid=1→"1"
		c.JSON(http.StatusOK, gin.H{"user_id": id})
	})
	return r
}

func doAuthReq(t *testing.T, r http.Handler, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/whoami", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuthMiddleware_NoHeader_401(t *testing.T) {
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	w := doAuthReq(t, r, "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_WrongPrefix_401(t *testing.T) {
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	tok, err := svc.GenerateToken(1)
	require.NoError(t, err)

	// 没有 "Bearer " 前缀
	w := doAuthReq(t, r, tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code)

	// 小写 "bearer " 不接受(严格大小写)
	w = doAuthReq(t, r, "bearer "+tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_EmptyToken_401(t *testing.T) {
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	w := doAuthReq(t, r, "Bearer ")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_InvalidToken_401(t *testing.T) {
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	w := doAuthReq(t, r, "Bearer garbage.token.here")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ExpiredToken_401(t *testing.T) {
	// TTL 为负,签出的 token 已过期
	expired := auth.NewJWTService(authMwTestSecret, -time.Hour)
	tok, err := expired.GenerateToken(1)
	require.NoError(t, err)

	// 用正常 TTL 的服务验证过期 token
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	w := doAuthReq(t, r, "Bearer "+tok)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestAuthMiddleware_ValidToken_200_UserIDSet(t *testing.T) {
	svc := auth.NewJWTService(authMwTestSecret, time.Hour)
	r := newAuthTestRouter(svc)

	tok, err := svc.GenerateToken(42)
	require.NoError(t, err)

	w := doAuthReq(t, r, "Bearer "+tok)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"user_id":42`)
}
