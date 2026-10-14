package web

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	memorysvc "omnibot/internal/service/memory"

	"github.com/gin-gonic/gin"
)

// JournalService 个人经历层窄接口(21 号 §5):时间轴 + 档案。
// 由 memoryService 实现,未注入时接口返回 503(向后兼容)。
type JournalService interface {
	Journal(ctx context.Context, userID int64, kind string, from, to *time.Time, cursor string, limit int) ([]memorysvc.JournalItem, string, error)
	Matters(userID int64, status string) ([]memorysvc.MatterSummary, error)
	MatterDetail(userID int64, id int64) (*memorysvc.MatterDetail, error)
	UpdateMatter(userID, id int64, stateDesc, status *string) (bool, error)
}

// SetJournalService 注入个人经历层服务。
func (h *Handler) SetJournalService(svc JournalService) {
	h.journalService = svc
}

// HandleGetJournal GET /api/v1/memories/journal — 时间轴(kind/from/to/cursor/limit)。
func (h *Handler) HandleGetJournal(c *gin.Context) {
	if h.journalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "个人经历层服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	parseTime := func(raw string) (*time.Time, error) {
		if raw == "" {
			return nil, nil
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	from, err := parseTime(c.Query("from"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "from 必须是 RFC3339 时间"})
		return
	}
	to, err := parseTime(c.Query("to"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "to 必须是 RFC3339 时间"})
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "0")) // 0 → service 落默认 50
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "limit 必须是整数"})
		return
	}
	items, next, err := h.journalService.Journal(c.Request.Context(), userID,
		c.DefaultQuery("kind", "all"), from, to, c.Query("cursor"), limit)
	if err != nil {
		mapJournalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"items": items, "next_cursor": next}})
}

// HandleListMatters GET /api/v1/matters?status=
func (h *Handler) HandleListMatters(c *gin.Context) {
	if h.journalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "个人经历层服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	list, err := h.journalService.Matters(userID, c.Query("status"))
	if err != nil {
		mapJournalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": list})
}

// HandleGetMatter GET /api/v1/matters/:id — 详情 + 演进时间线。
func (h *Handler) HandleGetMatter(c *gin.Context) {
	if h.journalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "个人经历层服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "档案 ID 非法"})
		return
	}
	detail, err := h.journalService.MatterDetail(userID, id)
	if err != nil {
		mapJournalErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": detail})
}

// updateMatterRequest 纠正口请求体:两字段都可选,指针区分"未提供"。
type updateMatterRequest struct {
	StateDesc *string `json:"state_desc"`
	Status    *string `json:"status"`
}

// HandleUpdateMatter PUT /api/v1/matters/:id — 用户纠正(state_desc/status)。
func (h *Handler) HandleUpdateMatter(c *gin.Context) {
	if h.journalService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "个人经历层服务未启用"})
		return
	}
	userID, ok := h.requireUserID(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "档案 ID 非法"})
		return
	}
	var req updateMatterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误"})
		return
	}
	okUpd, err := h.journalService.UpdateMatter(userID, id, req.StateDesc, req.Status)
	if err != nil {
		mapJournalErr(c, err)
		return
	}
	_ = okUpd // UpdateMatter 未命中已返回 ErrMatterNotFound
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func mapJournalErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, memorysvc.ErrJournalInvalidKind),
		errors.Is(err, memorysvc.ErrJournalInvalidCursor),
		errors.Is(err, memorysvc.ErrJournalInvalidStatus),
		errors.Is(err, memorysvc.ErrMatterNoFields),
		errors.Is(err, memorysvc.ErrMatterDescTooLong):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
	case errors.Is(err, memorysvc.ErrMatterNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "档案不存在"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "服务暂时不可用"})
	}
}
