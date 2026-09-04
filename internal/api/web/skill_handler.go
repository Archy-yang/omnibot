package web

import (
	"net/http"

	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
)

// SkillService 技能清单/启停窄接口(web 层声明,service 层实现)。
type SkillService interface {
	List() ([]skillsvc.SkillView, error)
	SetEnabled(name string, enabled bool) error
}

// SetSkillService 注入技能服务(技能管理 API)。未注入时技能接口返回 503。
func (h *Handler) SetSkillService(svc SkillService) {
	h.skillService = svc
}

// HandleListSkills GET /api/v1/skills — 技能清单(name/描述/来源/启停)。
func (h *Handler) HandleListSkills(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	views, err := h.skillService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取技能清单失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"skills": views,
		},
	})
}

// updateSkillRequest PUT /api/v1/skills/:name 请求体
type updateSkillRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

// HandleUpdateSkill PUT /api/v1/skills/:name — 启停技能(即时生效)。
func (h *Handler) HandleUpdateSkill(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	var req updateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请求体格式错误,需提供 enabled 布尔值"})
		return
	}
	name := c.Param("name")
	if err := h.skillService.SetEnabled(name, *req.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "更新技能状态失败"})
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
