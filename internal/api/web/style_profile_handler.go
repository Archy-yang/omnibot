package web

import (
	"net/http"
	"strconv"

	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
)

// StyleProfileService 风格与认知画像窄接口(24号 §5 + M2 注入开关;memoryService 实现)。
type StyleProfileService interface {
	StyleProfile(userID int64, limit int) (*memorysvc.StyleProfileView, error)
	DeleteStyleObservation(userID, id int64) (bool, error)
	DeleteCognitionChange(userID, id int64) (bool, error)
	// M2 注入开关(24号 §6 修订版:默认关,显式开启;关 = 完全回通用 persona)。
	GetStyleInjection(userID int64) (bool, error)
	SetStyleInjection(userID int64, enabled bool) error
}

// SetStyleProfileService 注入画像服务。未注入时接口返回 503(向后兼容)。
func (h *Handler) SetStyleProfileService(svc StyleProfileService) {
	h.styleProfileService = svc
}

// HandleGetStyleProfile GET /api/v1/style-profile — 画像视图(观察 + 认知变化)。
func (h *Handler) HandleGetStyleProfile(c *gin.Context) {
	if h.styleProfileService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "画像服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "0")) // 0 → service 默认 50
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit 必须是整数"})
		return
	}
	view, err := h.styleProfileService.StyleProfile(userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取画像失败"})
		return
	}
	// M2:注入开关态随视图返回(前端开关回显;读失败不阻断画像,按关处理)
	injectOn := false
	if on, err := h.styleProfileService.GetStyleInjection(userID); err == nil {
		injectOn = on
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view, "injection_enabled": injectOn})
}

// HandleUpdateStyleInjection PUT /api/v1/style-profile/injection — M2 注入开关。
func (h *Handler) HandleUpdateStyleInjection(c *gin.Context) {
	if h.styleProfileService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "画像服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"` // 指针:区分未传与显式 false
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体需含 enabled 布尔值"})
		return
	}
	if err := h.styleProfileService.SetStyleInjection(userID, *req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "写入开关失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "injection_enabled": *req.Enabled})
}

// HandleDeleteStyleObservation DELETE /api/v1/style-profile/observation/:id
func (h *Handler) HandleDeleteStyleObservation(c *gin.Context) {
	h.handleStyleDelete(c, h.styleProfileService.DeleteStyleObservation)
}

// HandleDeleteCognitionChange DELETE /api/v1/style-profile/change/:id
func (h *Handler) HandleDeleteCognitionChange(c *gin.Context) {
	h.handleStyleDelete(c, h.styleProfileService.DeleteCognitionChange)
}

type styleDeleteFunc func(userID, id int64) (bool, error)

func (h *Handler) handleStyleDelete(c *gin.Context, fn styleDeleteFunc) {
	if h.styleProfileService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "画像服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "ID 非法"})
		return
	}
	okDel, err := fn(userID, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "删除失败"})
		return
	}
	if !okDel {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "记录不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
