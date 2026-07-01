package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	userLLM "omnibot/internal/service/user"
)

// AuthService 认证服务接口(handler 依赖抽象,便于替换与测试)
type AuthService interface {
	Register(email, password string) (token string, err error)
	Login(email, password string) (token string, err error)
}

// AuthHandler 认证 API 处理器(注册/登录)
type AuthHandler struct {
	authService AuthService
}

// NewAuthHandler 创建认证处理器
func NewAuthHandler(authService AuthService) *AuthHandler {
	return &AuthHandler{authService: authService}
}

// 注册请求体(PRD 6.1)
type registerRequest struct {
	Email           string `json:"email" binding:"required"`
	Password        string `json:"password" binding:"required"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
}

// 登录请求体(PRD 6.2)
type loginRequest struct {
	Email    string `json:"email" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// 认证成功统一返回 token
type authResponse struct {
	Token string `json:"token"`
}

// HandleRegister POST /api/v1/auth/register
//
// 提示文案严格对齐 PRD 6.1 表格。
func (h *AuthHandler) HandleRegister(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入邮箱和密码"})
		return
	}
	if req.Password != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "两次输入的密码不一致"})
		return
	}
	token, err := h.authService.Register(req.Email, req.Password)
	if err != nil {
		status, body := mapRegisterError(err)
		c.JSON(status, body)
		return
	}
	c.JSON(http.StatusOK, authResponse{Token: token})
}

// HandleLogin POST /api/v1/auth/login
//
// 提示文案严格对齐 PRD 6.2 表格。
func (h *AuthHandler) HandleLogin(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请输入邮箱和密码"})
		return
	}
	token, err := h.authService.Login(req.Email, req.Password)
	if err != nil {
		status, body := mapLoginError(err)
		c.JSON(status, body)
		return
	}
	c.JSON(http.StatusOK, authResponse{Token: token})
}

// mapRegisterError 把 service sentinel error 映射为 HTTP status + PRD 定稿的用户提示
func mapRegisterError(err error) (int, gin.H) {
	switch {
	case errors.Is(err, userLLM.ErrEmailInvalid):
		return http.StatusBadRequest, gin.H{"error": "请输入有效的邮箱地址"}
	case errors.Is(err, userLLM.ErrPasswordInvalid):
		return http.StatusBadRequest, gin.H{"error": "密码长度需为 8~64 位"}
	case errors.Is(err, userLLM.ErrEmailAlreadyExists):
		return http.StatusConflict, gin.H{"error": "该邮箱已注册,请直接登录"}
	default:
		return http.StatusInternalServerError, gin.H{"error": "注册失败,请稍后重试"}
	}
}

// mapLoginError PRD 6.2 表
func mapLoginError(err error) (int, gin.H) {
	switch {
	case errors.Is(err, userLLM.ErrInvalidCredentials):
		return http.StatusUnauthorized, gin.H{"error": "邮箱或密码错误"}
	case errors.Is(err, userLLM.ErrAccountUnavailable):
		return http.StatusForbidden, gin.H{"error": "该账号不可用"}
	default:
		return http.StatusInternalServerError, gin.H{"error": "登录失败,请稍后重试"}
	}
}
