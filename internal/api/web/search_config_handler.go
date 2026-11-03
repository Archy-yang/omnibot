package web

// search_config_handler.go — 用户级联网搜索配置接口(32 号 §4.5)。
// 路由:/api/v1/user/search-config(GET/PUT/DELETE,均需登录态)。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"omnibot/internal/middleware"
	searchsvc "omnibot/internal/service/search"
)

// SearchConfigService 联网搜索配置服务(窄接口,便于 handler 单测)。
type SearchConfigService interface {
	GetView(userID int64) (*searchsvc.ConfigView, error)
	SaveConfig(userID int64, in searchsvc.SaveInput) (*searchsvc.ConfigView, error)
	ClearConfig(userID int64) error
}

// SetSearchConfigService 注入联网搜索配置服务。
func (h *Handler) SetSearchConfigService(svc SearchConfigService) {
	h.searchConfigService = svc
}

// updateSearchConfigRequest 保存请求体。
// 注意:没有 user_id 字段——身份只认 JWT,防止越权改他人配置。
type updateSearchConfigRequest struct {
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	APIKey         string `json:"api_key"`
	ClearKey       bool   `json:"clear_key"`
	Enabled        *bool  `json:"enabled"`
	DefaultCount   int    `json:"default_count"`
	NeedContent    *bool  `json:"need_content"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// HandleGetSearchConfig 读取配置(密钥脱敏;未配置时给默认初值)。
func (h *Handler) HandleGetSearchConfig(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未登录"})
		return
	}
	if h.searchConfigService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "联网搜索配置服务不可用"})
		return
	}

	view, err := h.searchConfigService.GetView(userID)
	if err != nil {
		// 不透传内部错误细节(安全红线)
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取配置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandleUpdateSearchConfig 保存配置(API Key 留空 = 保持原值)。
func (h *Handler) HandleUpdateSearchConfig(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未登录"})
		return
	}
	if h.searchConfigService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "联网搜索配置服务不可用"})
		return
	}

	var req updateSearchConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求格式不正确"})
		return
	}

	view, err := h.searchConfigService.SaveConfig(userID, searchsvc.SaveInput{
		Provider:       req.Provider,
		BaseURL:        req.BaseURL,
		APIKey:         req.APIKey,
		ClearKey:       req.ClearKey,
		Enabled:        req.Enabled,
		DefaultCount:   req.DefaultCount,
		NeedContent:    req.NeedContent,
		TimeoutSeconds: req.TimeoutSeconds,
	})
	if err != nil {
		// 校验错误是用户可纠正的,原文返回(domain 层文案已是中文且不含内部细节)
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": view})
}

// HandleDeleteSearchConfig 删除配置(回到未配置态)。
func (h *Handler) HandleDeleteSearchConfig(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	if userID <= 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "error": "未登录"})
		return
	}
	if h.searchConfigService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "联网搜索配置服务不可用"})
		return
	}

	if err := h.searchConfigService.ClearConfig(userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "删除配置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}
