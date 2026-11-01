package web

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"omnibot/internal/middleware"
	wechatclawsvc "omnibot/internal/service/wechatclaw"
)

// WeChatClawService 微信 ClawBot 扫码登录/状态服务(web 层窄接口,service 层实现)。
type WeChatClawService interface {
	StartLogin(ctx context.Context) (*wechatclawsvc.LoginView, error)
	PollLogin(ctx context.Context, qrcode, verifyCode string) (*wechatclawsvc.PollView, bool, error)
	Status() (*wechatclawsvc.StatusView, error)
	Disconnect(ctx context.Context) error
}

// WeChatClawHandler 微信 ClawBot 接入接口(31 号技术方案 §3.4)。
//
// 单账号语义:整个部署只有一个 ClawBot 身份,登录态随进程。接口仍要求登录态,
// 避免未认证用户触发扫码/断开。
type WeChatClawHandler struct {
	svc WeChatClawService
}

// NewWeChatClawHandler 创建处理器。svc 为 nil 时接口返回 503(向后兼容)。
func NewWeChatClawHandler(svc WeChatClawService) *WeChatClawHandler {
	return &WeChatClawHandler{svc: svc}
}

func (h *WeChatClawHandler) available(c *gin.Context) bool {
	if h.svc == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "微信 ClawBot 服务未启用"})
		return false
	}
	if c.GetInt64(middleware.AuthUserIDKey) <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未登录"})
		return false
	}
	return true
}

// HandleStartLogin POST /api/v1/wechat-claw/login
// 申请登录二维码,返回 qr_url(前端渲染成二维码给用户扫)。
func (h *WeChatClawHandler) HandleStartLogin(c *gin.Context) {
	if !h.available(c) {
		return
	}
	view, err := h.svc.StartLogin(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": "获取登录二维码失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandlePollLogin GET /api/v1/wechat-claw/login/:qrcode
// 轮询扫码状态;confirmed 时后端已完成凭证落库并拉起长轮询。
func (h *WeChatClawHandler) HandlePollLogin(c *gin.Context) {
	if !h.available(c) {
		return
	}
	qrcode := c.Param("qrcode")
	verifyCode := c.Query("verify_code")
	view, connected, err := h.svc.PollLogin(c.Request.Context(), qrcode, verifyCode)
	if err != nil {
		if errors.Is(err, wechatclawsvc.ErrQRExpired) {
			c.JSON(http.StatusGone, gin.H{"success": false, "error": "二维码已过期,请重新获取", "code": "qrcode_expired"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"success": false, "error": "查询扫码状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"data":      view,
		"connected": connected,
	})
}

// HandleGetStatus GET /api/v1/wechat-claw/status
func (h *WeChatClawHandler) HandleGetStatus(c *gin.Context) {
	if !h.available(c) {
		return
	}
	view, err := h.svc.Status()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取连接状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandleDisconnect DELETE /api/v1/wechat-claw
// 停止长轮询、通知服务端停止、清空凭证。
func (h *WeChatClawHandler) HandleDisconnect(c *gin.Context) {
	if !h.available(c) {
		return
	}
	if err := h.svc.Disconnect(c.Request.Context()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "断开连接失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
