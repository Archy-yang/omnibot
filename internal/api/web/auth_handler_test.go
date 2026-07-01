package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userLLM "omnibot/internal/service/user"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// mockAuthService 用于 handler 测试
type mockAuthService struct {
	registerFn func(email, password string) (string, error)
	loginFn    func(email, password string) (string, error)
}

func (m *mockAuthService) Register(email, password string) (string, error) {
	if m.registerFn != nil {
		return m.registerFn(email, password)
	}
	return "", errors.New("not implemented")
}

func (m *mockAuthService) Login(email, password string) (string, error) {
	if m.loginFn != nil {
		return m.loginFn(email, password)
	}
	return "", errors.New("not implemented")
}

func newAuthRouter(svc AuthService) *gin.Engine {
	r := gin.New()
	h := NewAuthHandler(svc)
	g := r.Group("/api/v1/auth")
	g.POST("/register", h.HandleRegister)
	g.POST("/login", h.HandleLogin)
	return r
}

func doJSONPost(t *testing.T, r http.Handler, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	buf, err := json.Marshal(body)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// ---------- Register ----------

func TestAuthHandler_Register_Success(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(email, password string) (string, error) {
			return "signed-token", nil
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":            "user@example.com",
		"password":         "password123",
		"confirm_password": "password123",
	}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"token":"signed-token"`)
}

func TestAuthHandler_Register_MissingFields(t *testing.T) {
	mock := &mockAuthService{}
	r := newAuthRouter(mock)

	// 缺 password
	body := map[string]string{"email": "user@example.com"}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入邮箱和密码")
}

func TestAuthHandler_Register_PasswordMismatch(t *testing.T) {
	mock := &mockAuthService{}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":            "user@example.com",
		"password":         "password123",
		"confirm_password": "password999",
	}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "两次输入的密码不一致")
}

func TestAuthHandler_Register_EmailInvalid(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(email, password string) (string, error) {
			return "", userLLM.ErrEmailInvalid
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":            "not-an-email",
		"password":         "password123",
		"confirm_password": "password123",
	}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入有效的邮箱地址")
}

func TestAuthHandler_Register_PasswordInvalid(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(email, password string) (string, error) {
			return "", userLLM.ErrPasswordInvalid
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":            "user@example.com",
		"password":         "short",
		"confirm_password": "short",
	}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "密码长度需为 8~64 位")
}

func TestAuthHandler_Register_EmailAlreadyExists(t *testing.T) {
	mock := &mockAuthService{
		registerFn: func(email, password string) (string, error) {
			return "", userLLM.ErrEmailAlreadyExists
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":            "dup@example.com",
		"password":         "password123",
		"confirm_password": "password123",
	}
	w := doJSONPost(t, r, "/api/v1/auth/register", body)
	assert.Equal(t, http.StatusConflict, w.Code)
	assert.Contains(t, w.Body.String(), "该邮箱已注册,请直接登录")
}

// ---------- Login ----------

func TestAuthHandler_Login_Success(t *testing.T) {
	mock := &mockAuthService{
		loginFn: func(email, password string) (string, error) {
			return "signed-token", nil
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":    "user@example.com",
		"password": "password123",
	}
	w := doJSONPost(t, r, "/api/v1/auth/login", body)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `"token":"signed-token"`)
}

func TestAuthHandler_Login_MissingFields(t *testing.T) {
	mock := &mockAuthService{}
	r := newAuthRouter(mock)

	body := map[string]string{"email": "user@example.com"}
	w := doJSONPost(t, r, "/api/v1/auth/login", body)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "请输入邮箱和密码")
}

func TestAuthHandler_Login_InvalidCredentials(t *testing.T) {
	mock := &mockAuthService{
		loginFn: func(email, password string) (string, error) {
			return "", userLLM.ErrInvalidCredentials
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":    "user@example.com",
		"password": "wrong",
	}
	w := doJSONPost(t, r, "/api/v1/auth/login", body)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Contains(t, w.Body.String(), "邮箱或密码错误")
}

func TestAuthHandler_Login_AccountUnavailable(t *testing.T) {
	mock := &mockAuthService{
		loginFn: func(email, password string) (string, error) {
			return "", userLLM.ErrAccountUnavailable
		},
	}
	r := newAuthRouter(mock)

	body := map[string]string{
		"email":    "banned@example.com",
		"password": "password123",
	}
	w := doJSONPost(t, r, "/api/v1/auth/login", body)
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "该账号不可用")
}
