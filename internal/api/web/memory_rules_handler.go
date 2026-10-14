package web

import (
	"errors"
	"net/http"
	"strconv"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
)

// MemoryRuleService 沉淀黑名单管理窄接口(22号 §5.3;memoryService 实现)。
type MemoryRuleService interface {
	ListBlacklist(userID int64) ([]*memorydomain.MemoryBlacklist, error)
	AddBlacklist(userID int64, pattern string) (*memorydomain.MemoryBlacklist, int64, error)
	RemoveBlacklist(userID, id int64) (bool, error)
}

// SetMemoryRuleService 注入黑名单管理服务。未注入时接口返回 503(向后兼容)。
func (h *Handler) SetMemoryRuleService(svc MemoryRuleService) {
	h.memoryRuleService = svc
}

// HandleListMemoryRules GET /api/v1/memory-rules — 规则清单。
func (h *Handler) HandleListMemoryRules(c *gin.Context) {
	if h.memoryRuleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "记忆规则服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	rules, err := h.memoryRuleService.ListBlacklist(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取规则失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": rules})
}

// addMemoryRuleRequest POST 请求体。
type addMemoryRuleRequest struct {
	Pattern string `json:"pattern" binding:"required"`
}

// HandleAddMemoryRule POST /api/v1/memory-rules — 新增规则(+删存量,回报条数)。
func (h *Handler) HandleAddMemoryRule(c *gin.Context) {
	if h.memoryRuleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "记忆规则服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	var req addMemoryRuleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "pattern 必填"})
		return
	}
	rule, deleted, err := h.memoryRuleService.AddBlacklist(userID, req.Pattern)
	if err != nil {
		mapMemoryRuleErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"rule": rule, "deleted": deleted}})
}

// HandleDeleteMemoryRule DELETE /api/v1/memory-rules/:id — 解除规则。
func (h *Handler) HandleDeleteMemoryRule(c *gin.Context) {
	if h.memoryRuleService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "记忆规则服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "规则 ID 非法"})
		return
	}
	okDel, err := h.memoryRuleService.RemoveBlacklist(userID, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "删除规则失败"})
		return
	}
	if !okDel {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "规则不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func mapMemoryRuleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, memorysvc.ErrBlacklistInvalidPattern):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "pattern 不能为空且不超过 128 字"})
	case errors.Is(err, memoryrepo.ErrBlacklistDuplicate):
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "规则已存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "服务暂时不可用"})
	}
}
