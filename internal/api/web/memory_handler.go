package web

// memory_handler.go — 长期记忆接口(自 handler.go 按资源拆出,方法仍在 Handler 上)。
// 路由:/api/v1/memories(见 routes.go)。

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/internal/middleware"
	memorysvc "omnibot/internal/service/memory"
)

// ========== 长期记忆接口 ==========

type MemoryDTO struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	Source    string `json:"source"` // manual=用户交代 / auto=沉淀管线提取(注入分层,前端双 tab)
	CreatedAt string `json:"created_at"`
}

type GetMemoriesResponse struct {
	Memories []MemoryDTO `json:"memories"`
}

type CreateMemoryRequest struct {
	Content string `json:"content" binding:"required"`
}

type CreateMemoryResponse struct {
	Message string    `json:"message"`
	Memory  MemoryDTO `json:"memory"`
}

type ClearMemoriesResponse struct {
	Message string `json:"message"`
}

type DeleteMemoryURIRequest struct {
	MemoryID int64 `uri:"id" binding:"required,min=1"`
}

type DeleteMemoryResponse struct {
	Message string `json:"message"`
}

type UpdateMemoryURIRequest struct {
	MemoryID int64 `uri:"id" binding:"required,min=1"`
}

type UpdateMemoryRequest struct {
	Content string `json:"content" binding:"required"`
}

type UpdateMemoryResponse struct {
	Message string    `json:"message"`
	Memory  MemoryDTO `json:"memory"`
}

func toMemoryDTO(memory *memorydomain.Memory) MemoryDTO {
	source := memory.Source
	if source == "" {
		source = memorydomain.MemorySourceManual // 迁移期兜底:老数据视为手动
	}
	return MemoryDTO{
		ID:        memory.ID,
		Content:   memory.Content,
		Source:    source,
		CreatedAt: memory.CreatedAt.Format(time.RFC3339),
	}
}

// HandleGetMemories 获取用户的全部长期记忆
func (h *Handler) HandleGetMemories(c *gin.Context) {
	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	memories, err := h.memoryService.List(c.Request.Context(), userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	items := make([]MemoryDTO, 0, len(memories))
	for _, memory := range memories {
		items = append(items, toMemoryDTO(memory))
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": GetMemoriesResponse{
			Memories: items,
		},
	})
}

// HandleCreateMemory 新增一条长期记忆
func (h *Handler) HandleCreateMemory(c *gin.Context) {
	var req CreateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "请求参数错误",
		})
		return
	}

	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	memory, err := h.memoryService.Remember(c.Request.Context(), userID, req.Content)
	if err != nil {
		status := http.StatusInternalServerError
		message := "服务暂时不可用，请稍后再试。"
		if errors.Is(err, memorysvc.ErrEmptyContent) {
			status = http.StatusBadRequest
			message = "请输入要长期记住的内容。"
		}
		if errors.Is(err, memorysvc.ErrContentTooLong) {
			status = http.StatusBadRequest
			message = "这条记忆太长了，请控制在 200 字以内。"
		}
		c.JSON(status, gin.H{
			"success": false,
			"error":   message,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": CreateMemoryResponse{
			Message: "已记住。",
			Memory:  toMemoryDTO(memory),
		},
	})
}

// HandleClearMemories 清空长期记忆。带 ?source=manual|auto 时只清该来源
// (记忆抽屉双 tab 各清各的);不带参数清空全部(兼容渠道 #清空记忆 语义)。
func (h *Handler) HandleClearMemories(c *gin.Context) {
	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	message := "已清空你的全部长期记忆。"
	clearFn := func() error { return h.memoryService.Clear(c.Request.Context(), userID) }
	if source := c.Query("source"); source != "" {
		if source != memorydomain.MemorySourceManual && source != memorydomain.MemorySourceAuto {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   "无效的记忆来源。",
			})
			return
		}
		src := source // 捕获
		clearFn = func() error { return h.memoryService.ClearSource(c.Request.Context(), userID, src) }
		if source == memorydomain.MemorySourceManual {
			message = "已清空你手动添加的记忆。"
		} else {
			message = "已清空全部自动沉淀的记忆。"
		}
	}

	if err := clearFn(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": ClearMemoriesResponse{
			Message: message,
		},
	})
}

// HandleDeleteMemory 删除单条长期记忆
func (h *Handler) HandleDeleteMemory(c *gin.Context) {
	var uriReq DeleteMemoryURIRequest
	if err := c.ShouldBindUri(&uriReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "无效的记忆 ID。",
		})
		return
	}

	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	deleted, err := h.memoryService.Delete(c.Request.Context(), userID, uriReq.MemoryID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	if !deleted {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "记忆不存在或不属于当前用户。",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": DeleteMemoryResponse{
			Message: "已删除记忆。",
		},
	})
}

// HandleUpdateMemory 更新单条长期记忆
func (h *Handler) HandleUpdateMemory(c *gin.Context) {
	var uriReq UpdateMemoryURIRequest
	if err := c.ShouldBindUri(&uriReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "无效的记忆 ID。",
		})
		return
	}

	var req UpdateMemoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "请求参数错误",
		})
		return
	}

	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	memory, err := h.memoryService.Update(c.Request.Context(), userID, uriReq.MemoryID, req.Content)
	if err != nil {
		status := http.StatusInternalServerError
		message := "服务暂时不可用，请稍后再试。"
		if errors.Is(err, memorysvc.ErrEmptyContent) {
			status = http.StatusBadRequest
			message = "请输入要长期记住的内容。"
		}
		if errors.Is(err, memorysvc.ErrContentTooLong) {
			status = http.StatusBadRequest
			message = "这条记忆太长了，请控制在 200 字以内。"
		}
		c.JSON(status, gin.H{
			"success": false,
			"error":   message,
		})
		return
	}

	if memory == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "记忆不存在或不属于当前用户。",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": UpdateMemoryResponse{
			Message: "已更新记忆。",
			Memory:  toMemoryDTO(memory),
		},
	})
}
