package web

// persistent_task_handler.go — 持久任务 API(25 号 §6):
// 列表 + 控制(pause/resume/delete);创建走对话内工具(自然语言→cron 由 LLM 换算),
// 前端不做创建表单。

import (
	"errors"
	"net/http"
	"strconv"

	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/middleware"
	agentsvc "omnibot/internal/service/agent"

	"github.com/gin-gonic/gin"
)

// PersistentTaskAPIService 持久任务服务接口(handler 依赖窄面,service/agent 实现)。
type PersistentTaskAPIService interface {
	List(userID int64) ([]*agentdomain.PersistentTask, error)
	Pause(userID, id int64) error
	Resume(userID, id int64) error
	Delete(userID, id int64) error
}

// PersistentTaskView 列表视图(不含 TaskSpecJSON 等内部字段)。
// 内嵌 domain 实体可序列化,但显式 DTO 防止未来实体加内部字段时泄露(黑名单 json tag 教训)。
type PersistentTaskView struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Goal        string     `json:"goal"`
	Cron        string     `json:"cron"`
	Enabled     bool       `json:"enabled"`
	NextRunAt   string     `json:"next_run_at"`
	LastRunAt   *string    `json:"last_run_at"`
	LastTaskID  *int64     `json:"last_task_id"`
	FailStreak  int        `json:"fail_streak"`
	AutoPaused  bool       `json:"auto_paused"` // 连续失败达到阈值被自动停用(前端展示专用标记)
	CreatedAt   string     `json:"created_at"`
}

// SetPersistentTaskService 注入持久任务服务(wire.go)。
func (h *Handler) SetPersistentTaskService(svc PersistentTaskAPIService) {
	h.persistentTaskService = svc
}

// HandleListPersistentTasks GET /api/v1/persistent-tasks
func (h *Handler) HandleListPersistentTasks(c *gin.Context) {
	if h.persistentTaskService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "持久任务服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	list, err := h.persistentTaskService.List(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询持久任务失败"})
		return
	}
	views := make([]*PersistentTaskView, 0, len(list))
	for _, pt := range list {
		views = append(views, toPersistentTaskView(pt))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "tasks": views})
}

// HandleControlPersistentTask POST /api/v1/persistent-tasks/:id/pause|resume
func (h *Handler) HandleControlPersistentTask(c *gin.Context) {
	if h.persistentTaskService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "持久任务服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的任务 ID"})
		return
	}

	var ctrl func(int64, int64) error
	switch c.Param("action") {
	case "pause":
		ctrl = h.persistentTaskService.Pause
	case "resume":
		ctrl = h.persistentTaskService.Resume
	default:
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "未知操作"})
		return
	}
	if err := ctrl(userID, id); err != nil {
		respondPersistentTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// HandleDeletePersistentTask DELETE /api/v1/persistent-tasks/:id
func (h *Handler) HandleDeletePersistentTask(c *gin.Context) {
	if h.persistentTaskService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "持久任务服务未装配"})
		return
	}
	userID := c.GetInt64(middleware.AuthUserIDKey)
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的任务 ID"})
		return
	}
	if err := h.persistentTaskService.Delete(userID, id); err != nil {
		respondPersistentTaskError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func toPersistentTaskView(pt *agentdomain.PersistentTask) *PersistentTaskView {
	v := &PersistentTaskView{
		ID: pt.ID, Name: pt.Name, Goal: pt.Goal, Cron: pt.Cron,
		Enabled: pt.Enabled, NextRunAt: pt.NextRunAt.Format("2006-01-02 15:04"),
		LastTaskID: pt.LastTaskID, FailStreak: pt.FailStreak,
		AutoPaused: !pt.Enabled && pt.FailStreak >= agentdomain.AutoPauseFailStreak,
		CreatedAt:  pt.CreatedAt.Format("2006-01-02 15:04"),
	}
	if pt.LastRunAt != nil {
		s := pt.LastRunAt.Format("2006-01-02 15:04")
		v.LastRunAt = &s
	}
	return v
}

func respondPersistentTaskError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, agentsvc.ErrPersistentTaskNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "任务不存在或已删除"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "操作失败"})
	}
}
