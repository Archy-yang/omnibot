package web

import (
	"context"
	"net/http"
	"strconv"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
)

// MCPManager MCP server 在线管理窄接口(web 层声明,service 层实现)。
type MCPManager interface {
	ListServers() ([]skilldomain.ServerView, error)
	AddServer(in skillsvc.MCPServerInput) (*skilldomain.ServerView, error)
	UpdateServer(id int64, in skillsvc.MCPServerInput) (*skilldomain.ServerView, error)
	DeleteServer(id int64) error
	SyncServer(id int64) (*skillsvc.SyncResult, error)
	// BeginOAuth 生成授权 URL(M4;state 挂起服务端等回调)。
	BeginOAuth(ctx context.Context, id int64) (*skillsvc.OAuthBeginResult, error)
	// HandleOAuthCallback 处理服务商重定向回调(state 校验+换 token 落库)。
	HandleOAuthCallback(ctx context.Context, code, state string) error
}

// SetMCPManager 注入 MCP 管理服务。未注入时接口返回 503。
func (h *Handler) SetMCPManager(mgr MCPManager) {
	h.mcpManager = mgr
}

// upsertMCPServerRequest 创建/更新请求体(api_key/client_secret 空 = 保留原值)。
type upsertMCPServerRequest struct {
	Name              string `json:"name" binding:"required"`
	BaseURL           string `json:"base_url" binding:"required"`
	APIKey            string `json:"api_key"`
	AuthType          string `json:"auth_type"` // none/bearer/oauth,空 = bearer
	OAuthClientID     string `json:"oauth_client_id"`
	OAuthClientSecret string `json:"oauth_client_secret"`
	OAuthScopes       string `json:"oauth_scopes"`
	Enabled           *bool  `json:"enabled" binding:"required"`
}

// toInput 请求体 → service 入参。
func (r *upsertMCPServerRequest) toInput() skillsvc.MCPServerInput {
	return skillsvc.MCPServerInput{
		Name: r.Name, BaseURL: r.BaseURL, APIKey: r.APIKey,
		AuthType: r.AuthType, OAuthClientID: r.OAuthClientID,
		OAuthClientSecret: r.OAuthClientSecret, OAuthScopes: r.OAuthScopes,
		Enabled: *r.Enabled,
	}
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
	view, err := h.mcpManager.AddServer(req.toInput())
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
	view, err := h.mcpManager.UpdateServer(id, req.toInput())
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

// HandleAuthorizeMCPServer POST /api/v1/mcp/servers/:id/authorize — 发起 OAuth 授权。
// 返回授权 URL,前端新窗口打开;服务商授权后重定向到 /api/v1/mcp/oauth/callback。
func (h *Handler) HandleAuthorizeMCPServer(c *gin.Context) {
	if h.mcpManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "MCP 管理未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的服务 ID"})
		return
	}
	res, err := h.mcpManager.BeginOAuth(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{
		"authorization_url": res.AuthorizationURL,
		"state":             res.State,
	}})
}

// HandleOAuthCallback GET /api/v1/mcp/oauth/callback — 服务商重定向落点(浏览器直达,不挂 JWT)。
// 安全性由一次性 state(10 分钟 TTL)保障;结果以简单 HTML 呈现给用户。
func (h *Handler) HandleOAuthCallback(c *gin.Context) {
	if h.mcpManager == nil {
		c.String(http.StatusServiceUnavailable, "MCP 管理未启用")
		return
	}
	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		c.String(http.StatusBadRequest, "授权回调缺少参数,请返回技能页重试")
		return
	}
	if err := h.mcpManager.HandleOAuthCallback(c.Request.Context(), code, state); err != nil {
		c.String(http.StatusBadRequest, "授权失败:%s\n请返回技能页重新发起授权。", err.Error())
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, "<!doctype html><meta charset=\"utf-8\"><title>授权成功</title>"+
		"<p style=\"font-family:sans-serif;font-size:16px\">✅ 授权成功,请返回 OmniBot 技能页,点击「同步」发现该服务提供的技能。</p>")
}
