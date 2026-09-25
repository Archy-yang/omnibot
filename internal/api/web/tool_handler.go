package web

import (
	"net/http"

	toolsvc "omnibot/internal/service/tool"

	"github.com/gin-gonic/gin"
)

// ToolService 工具清单/启停窄接口(web 层声明,service 层实现)。
type ToolService interface {
	List() ([]toolsvc.ToolView, error)
	SetEnabled(name string, enabled bool) error
}

// SetToolService 注入工具服务(工具管理 API)。未注入时工具接口返回 503。
func (h *Handler) SetToolService(svc ToolService) {
	h.toolService = svc
}

// HandleListTools GET /api/v1/tools — 工具清单(name/描述/来源/启停)。
func (h *Handler) HandleListTools(c *gin.Context) {
	if h.toolService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "工具服务未启用"})
		return
	}
	views, err := h.toolService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取工具清单失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"tools": views,
		},
	})
}

// updateToolRequest PUT /api/v1/tools/:name 请求体
type updateToolRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// HandleUpdateTool PUT /api/v1/tools/:name — 启停工具(即时生效)。
func (h *Handler) HandleUpdateTool(c *gin.Context) {
	if h.toolService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "工具服务未启用"})
		return
	}
	var req updateToolRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误,需提供 enabled 布尔值"})
		return
	}
	name := c.Param("name")
	if err := h.toolService.SetEnabled(name, *req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "更新工具状态失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"name":    name,
			"enabled": *req.Enabled,
		},
	})
}
