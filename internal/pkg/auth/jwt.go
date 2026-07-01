// Package auth 提供 JWT 签发与解析,供中间件与 AuthService 使用。
//
// 设计要点:
//   - HS256 对称签名,单服务足够,无需公私钥
//   - Claims 只放 user_id,不塞用户敏感信息(如邮箱/角色),减小 token 体积与泄漏面
//   - Parse 失败一律归并为 error,不区分过期/签名错/格式错,防止给攻击者信号
//   - 显式限定 HMAC 签名算法,防 alg=none 攻击
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims 自定义载荷:标准声明(iat/exp) + user_id
type Claims struct {
	UserID int64 `json:"user_id"`
	jwt.RegisteredClaims
}

// JWTService 签发与解析 JWT
type JWTService struct {
	secret []byte
	ttl    time.Duration
}

// NewJWTService 创建 JWT 服务。
// secret 从 config 读入,不硬编码;ttl 建议 30 天(v2.1 PRD 5.3)。
func NewJWTService(secret string, ttl time.Duration) *JWTService {
	return &JWTService{secret: []byte(secret), ttl: ttl}
}

// GenerateToken 签发 token,payload 含 user_id + iat + exp。
func (s *JWTService) GenerateToken(userID int64) (string, error) {
	now := time.Now()
	claims := Claims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.ttl)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(s.secret)
}

// ParseToken 验签 + 验过期,返回 userID。
// 任何失败(签名错/过期/格式错)统一返回 error,由上层 middleware 一律回 401。
func (s *JWTService) ParseToken(tokenStr string) (int64, error) {
	claims := &Claims{}
	tok, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
		// 显式限定 HMAC 家族,防 alg=none 或 RS256 伪造
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.secret, nil
	})
	if err != nil {
		return 0, err
	}
	if !tok.Valid {
		return 0, errors.New("invalid token")
	}
	return claims.UserID, nil
}
