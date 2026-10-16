package web

// proactive_handler.go — 主动管家设置 API(26 号 §5):
// GET/PUT /api/v1/proactive-settings(开关+时刻)+ GET /api/v1/proactive-notes(最近记录)。
// 归属隔离:userID 一律取自 auth,不信参数。

import (
	"net/http"
	"strconv"

	butlerdomain "omnibot/internal/domain/butler"
	"omnibot/internal/middleware"

	"github.com/gin-gonic/gin"
)

// ProactiveSettingsAPIService 设置服务窄面(service/butler 实现)。
type ProactiveSettingsAPIService interface {
	GetSettings(userID int64) (enabled bool, hour int, err error)
	UpdateSettings(userID int64, enabled bool, hour int) error
	ListNotes(userID int64, limit int) ([]*butlerdomain.ProactiveNote, error)
}

// SetProactiveService 注入主动管家设置服务(wire.go)。
func (h *Handler) SetProactiveService(svc ProactiveSettingsAPIService) {
	h.proactiveService = svc
}

// HandleGetProactiveSettings GET /api/v1/proactive-settings
func (h *Handler) HandleGetProactiveSettings(c *gin.Context) {
	if h.proactiveService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "主动管家服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	enabled, hour, err := h.proactiveService.GetSettings(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取设置失败"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": enabled, "hour": hour})
}

// HandleUpdateProactiveSettings PUT /api/v1/proactive-settings
func (h *Handler) HandleUpdateProactiveSettings(c *gin.Context) {
	if h.proactiveService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "主动管家服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	var req struct {
		Enabled bool `json:"enabled"`
		Hour    *int `json:"hour"` // 指针区分"未传"(保留现值)与传 0(午夜)
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误"})
		return
	}
	var hour int
	if req.Hour != nil {
		hour = *req.Hour
	} else {
		_, cur, err := h.proactiveService.GetSettings(userID)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "读取设置失败"})
			return
		}
		hour = cur
	}
	if err := h.proactiveService.UpdateSettings(userID, req.Enabled, hour); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "enabled": req.Enabled, "hour": hour})
}

// ProactiveNoteView 最近记录视图(显式 DTO,不泄露内部字段)。
type ProactiveNoteView struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	DedupKey  string `json:"dedup_key"`
	MatterID  *int64 `json:"matter_id"`
	CreatedAt string `json:"created_at"`
}

// HandleListProactiveNotes GET /api/v1/proactive-notes?limit=20
func (h *Handler) HandleListProactiveNotes(c *gin.Context) {
	if h.proactiveService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "主动管家服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	limit := 20
	if v, err := strconv.Atoi(c.Query("limit")); err == nil && v > 0 && v <= 50 {
		limit = v
	}
	list, err := h.proactiveService.ListNotes(userID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询记录失败"})
		return
	}
	views := make([]ProactiveNoteView, 0, len(list))
	for _, n := range list {
		views = append(views, ProactiveNoteView{
			ID: n.ID, Kind: n.Kind, Content: n.Content, Status: n.Status,
			DedupKey: n.DedupKey, MatterID: n.MatterID,
			CreatedAt: n.CreatedAt.Format("2006-01-02 15:04"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "notes": views})
}
