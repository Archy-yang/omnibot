package web

import (
	"context"
	"errors"
	"net/http"

	"omnibot/internal/middleware"
	larkauth "omnibot/internal/service/larkauth"

	"github.com/gin-gonic/gin"
)

// LarkAuthManager 用户级飞书授权窄接口(web 层声明,service 层实现)。
type LarkAuthManager interface {
	StartAuth(ctx context.Context, userID int64, appID, appSecret string) (larkauth.View, error)
	PollAuth(ctx context.Context, userID int64) (larkauth.View, error)
	View(userID int64) (larkauth.View, error)
	Disconnect(ctx context.Context, userID int64) error
}

// SetLarkAuthService 注入授权服务。未注入时接口返回 503。
func (h *Handler) SetLarkAuthService(svc LarkAuthManager) {
	h.larkAuthService = svc
}

// requireUserID 取 JWT 中间件注入 gin.Context 的用户 id(AuthUserIDKey,与各 handler 同源);缺失回 401。
func (h *Handler) requireUserID(c *gin.Context) (int64, bool) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未登录"})
		return 0, false
	}
	return userID, true
}

// HandleGetFeishuAuth GET /api/v1/feishu-auth — 当前用户授权状态(纯读,不触发收尾)。
func (h *Handler) HandleGetFeishuAuth(c *gin.Context) {
	if h.larkAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "飞书授权服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	view, err := h.larkAuthService.View(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取授权状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// startFeishuAuthRequest POST 请求体。
type startFeishuAuthRequest struct {
	AppID     string `json:"app_id" binding:"required"`
	AppSecret string `json:"app_secret" binding:"required"`
}

// HandleStartFeishuAuth POST /api/v1/feishu-auth — 发起授权(返回验证地址,用户浏览器确认)。
func (h *Handler) HandleStartFeishuAuth(c *gin.Context) {
	if h.larkAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "飞书授权服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	var req startFeishuAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误(app_id/app_secret 必填)"})
		return
	}
	view, err := h.larkAuthService.StartAuth(c.Request.Context(), userID, req.AppID, req.AppSecret)
	if err != nil {
		mapLarkAuthErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandlePollFeishuAuth GET /api/v1/feishu-auth/poll — 惰性收尾 + 状态(前端轮询)。
func (h *Handler) HandlePollFeishuAuth(c *gin.Context) {
	if h.larkAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "飞书授权服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	view, err := h.larkAuthService.PollAuth(c.Request.Context(), userID)
	if err != nil {
		mapLarkAuthErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandleDisconnectFeishuAuth DELETE /api/v1/feishu-auth — 断开授权(logout + 删行)。
func (h *Handler) HandleDisconnectFeishuAuth(c *gin.Context) {
	if h.larkAuthService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "飞书授权服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	if err := h.larkAuthService.Disconnect(c.Request.Context(), userID); err != nil {
		mapLarkAuthErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func mapLarkAuthErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, larkauth.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "app_id 与 app_secret 必填"})
	case errors.Is(err, larkauth.ErrCryptoAbsent):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "服务端加密密钥未配置,无法保存凭证"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": err.Error()})
	}
}
