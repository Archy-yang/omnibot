package user

import (
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/auth"
)

const authTestSecret = "test-secret-32-chars-min-len-ok!"

func setupAuthTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	err = db.AutoMigrate(&domain.User{}, &domain.UserChannel{}, &domain.UserCredential{})
	require.NoError(t, err)
	return db
}

func newAuthService(t *testing.T) *AuthService {
	db := setupAuthTestDB(t)
	jwtSvc := auth.NewJWTService(authTestSecret, time.Hour)
	return NewAuthService(db, jwtSvc)
}

// ---------- Register ----------

func TestAuthService_Register_Success(t *testing.T) {
	svc := newAuthService(t)

	token, err := svc.Register("user@example.com", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)

	// token 里应能解出 user_id > 0
	jwtSvc := auth.NewJWTService(authTestSecret, time.Hour)
	uid, err := jwtSvc.ParseToken(token)
	require.NoError(t, err)
	assert.Greater(t, uid, int64(0))
}

func TestAuthService_Register_DuplicateEmail(t *testing.T) {
	svc := newAuthService(t)

	_, err := svc.Register("dup@example.com", "password123")
	require.NoError(t, err)

	_, err = svc.Register("dup@example.com", "password456")
	assert.ErrorIs(t, err, ErrEmailAlreadyExists)
}

func TestAuthService_Register_EmailNormalization(t *testing.T) {
	svc := newAuthService(t)

	// Foo@X.com 归一化后与 foo@x.com 相同,应视为重复
	_, err := svc.Register("Foo@X.com", "password123")
	require.NoError(t, err)

	_, err = svc.Register("  foo@x.com  ", "password456")
	assert.ErrorIs(t, err, ErrEmailAlreadyExists)
}

func TestAuthService_Register_EmailInvalid(t *testing.T) {
	svc := newAuthService(t)

	cases := []string{
		"",             // 空
		"noatsign.com", // 无 @
		"a@b",          // 域名无 .
		"a@@b.com",     // 多个 @
	}
	for _, e := range cases {
		_, err := svc.Register(e, "password123")
		assert.ErrorIs(t, err, ErrEmailInvalid, "email=%q", e)
	}

	// 超长邮箱(> 254)
	longEmail := ""
	for i := 0; i < 250; i++ {
		longEmail += "a"
	}
	longEmail += "@x.com"
	_, err := svc.Register(longEmail, "password123")
	assert.ErrorIs(t, err, ErrEmailInvalid)
}

func TestAuthService_Register_PasswordInvalid(t *testing.T) {
	svc := newAuthService(t)

	// 7 位太短
	_, err := svc.Register("a@b.com", "1234567")
	assert.ErrorIs(t, err, ErrPasswordInvalid)

	// 65 位太长
	pw65 := ""
	for i := 0; i < 65; i++ {
		pw65 += "a"
	}
	_, err = svc.Register("b@b.com", pw65)
	assert.ErrorIs(t, err, ErrPasswordInvalid)
}

// ---------- Login ----------

func TestAuthService_Login_Success(t *testing.T) {
	svc := newAuthService(t)

	_, err := svc.Register("user@example.com", "password123")
	require.NoError(t, err)

	token, err := svc.Login("user@example.com", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestAuthService_Login_EmailNotFound(t *testing.T) {
	svc := newAuthService(t)

	_, err := svc.Login("noone@example.com", "password123")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthService_Login_WrongPassword(t *testing.T) {
	svc := newAuthService(t)

	_, err := svc.Register("user@example.com", "password123")
	require.NoError(t, err)

	_, err = svc.Login("user@example.com", "wrongpassword")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}

func TestAuthService_Login_EmailCaseInsensitive(t *testing.T) {
	svc := newAuthService(t)

	_, err := svc.Register("user@example.com", "password123")
	require.NoError(t, err)

	// 大写邮箱登录也能命中
	token, err := svc.Login("USER@Example.COM", "password123")
	require.NoError(t, err)
	assert.NotEmpty(t, token)
}

func TestAuthService_Login_BannedUser(t *testing.T) {
	db := setupAuthTestDB(t)
	jwtSvc := auth.NewJWTService(authTestSecret, time.Hour)
	svc := NewAuthService(db, jwtSvc)

	_, err := svc.Register("banned@example.com", "password123")
	require.NoError(t, err)

	// 手工把用户置为封禁
	err = db.Model(&domain.User{}).Where("id = ?", 1).Update("status", domain.StatusBanned).Error
	require.NoError(t, err)

	_, err = svc.Login("banned@example.com", "password123")
	assert.ErrorIs(t, err, ErrAccountUnavailable)
}

// ---------- Register 事务回滚 ----------

func TestAuthService_Register_PasswordSpaceIsSignificant(t *testing.T) {
	// PRD 5.2:密码不去空格,首尾空格是密码一部分
	svc := newAuthService(t)

	_, err := svc.Register("space@example.com", "pass word") // 中间有空格
	require.NoError(t, err)

	// 用相同密码能登录
	_, err = svc.Login("space@example.com", "pass word")
	assert.NoError(t, err)

	// 去掉空格后不能登录
	_, err = svc.Login("space@example.com", "password")
	assert.ErrorIs(t, err, ErrInvalidCredentials)
}
