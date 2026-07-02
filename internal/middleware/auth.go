package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"omnibot/internal/pkg/auth"
)

const (
	// AuthUserIDKey gin.Context 中注入的 user_id 键名
	// handler 层通过 c.GetInt64(AuthUserIDKey) 取当前登录用户
	AuthUserIDKey = "user_id"

	bearerPrefix = "Bearer "
)

// AuthRequired JWT 鉴权中间件
//
// 挂载:业务接口路由组(不含注册/登录/health/config)。
// 成功:c.Set("user_id", int64) → 交由 handler 使用 c.GetInt64("user_id")。
// 失败:401,不区分具体原因(过期/签名错/格式错),防信号泄漏。
func AuthRequired(jwtSvc *auth.JWTService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, bearerPrefix) {
			unauthorized(c)
			return
		}
		token := strings.TrimPrefix(header, bearerPrefix)
		if token == "" {
			unauthorized(c)
			return
		}
		userID, err := jwtSvc.ParseToken(token)
		if err != nil {
			unauthorized(c)
			return
		}
		c.Set(AuthUserIDKey, userID)
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"})
}
