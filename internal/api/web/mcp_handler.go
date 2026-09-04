package web

import (
	"net/http"
	"strconv"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
)

// MCPManager MCP server 在线管理窄接口(web 层声明,service 层实现)。
type MCPManager interface {
	ListServers() ([]skilldomain.ServerView, error)
	AddServer(name, baseURL, apiKey string, enabled bool) (*skilldomain.ServerView, error)
	UpdateServer(id int64, name, baseURL, apiKey string, enabled bool) (*skilldomain.ServerView, error)
	DeleteServer(id int64) error
	SyncServer(id int64) (*skillsvc.SyncResult, error)
}

// SetMCPManager 注入 MCP 管理服务。未注入时接口返回 503。
func (h *Handler) SetMCPManager(mgr MCPManager) {
	h.mcpManager = mgr
}

// upsertMCPServerRequest 创建/更新请求体(api_key 空 = 保留原值)。
type upsertMCPServerRequest struct {
	Name    string `json:"name" binding:"required"`
	BaseURL string `json:"base_url" binding:"required"`
	APIKey  string `json:"api_key"`
	Enabled *bool  `json:"enabled" binding:"required"`
}

// HandleListMCPServers GET /api/v1/mcp/servers — server 清单(密钥只回显 has_api_key)。
func (h *Handler) HandleListMCPServers(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	views, err := h.mcpManager.ListServers()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取服务清单失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"servers": views}})
}

// HandleCreateMCPServer POST /api/v1/mcp/servers — 新增并立即同步。
// 业务校验失败(重名/地址非法)返回 400 + 可读错误;同步失败不阻断(可手动重试)。
func (h *Handler) HandleCreateMCPServer(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	var req upsertMCPServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请填写服务名称和地址"})
		return
	}
	view, err := h.mcpManager.AddServer(req.Name, req.BaseURL, req.APIKey, *req.Enabled)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"server": view}})
}

// HandleUpdateMCPServer PUT /api/v1/mcp/servers/:id — 更新并按需重新同步。
func (h *Handler) HandleUpdateMCPServer(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的服务 ID"})
		return
	}
	var req upsertMCPServerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请填写服务名称和地址"})
		return
	}
	view, err := h.mcpManager.UpdateServer(id, req.Name, req.BaseURL, req.APIKey, *req.Enabled)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"server": view}})
}

// HandleDeleteMCPServer DELETE /api/v1/mcp/servers/:id — 删除并级联清理技能。
func (h *Handler) HandleDeleteMCPServer(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的服务 ID"})
		return
	}
	if err := h.mcpManager.DeleteServer(id); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"id": id}})
}

// HandleSyncMCPServer POST /api/v1/mcp/servers/:id/sync — 手动重新发现工具。
func (h *Handler) HandleSyncMCPServer(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的服务 ID"})
		return
	}
	result, err := h.mcpManager.SyncServer(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"server_name": result.ServerName,
		"tool_count":  result.ToolCount,
		"err":         result.Err,
	}})
}
