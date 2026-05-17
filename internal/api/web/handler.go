// Package web implements the Web chat API handlers
package web

import (
	"context"
	"net/http"
	"time"

	"omnibot/internal/client/llm"
	domainuser "omnibot/internal/domain/user"

	"github.com/gin-gonic/gin"
)

// UserService 用户服务接口
type UserService interface {
	GetOrCreateByChannel(channelType string, channelUserID string) (*domainuser.User, *domainuser.UserChannel, bool, error)
}

// MessageService 消息服务接口
type MessageService interface {
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)
	SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error
	SaveAssistantMessage(ctx context.Context, userID int64, content string) error
}

// LLMClient 大模型客户端接口
type LLMClient interface {
	ChatCompletion(ctx context.Context, messages []llm.ChatMessage) (string, error)
}

// Handler Web 聊天 API 处理器
type Handler struct {
	userService    UserService
	messageService MessageService
	llmClient      LLMClient
}

// NewHandler 创建 Web 聊天处理器
func NewHandler(
	userService UserService,
	messageService MessageService,
	llmClient LLMClient,
) *Handler {
	return &Handler{
		userService:    userService,
		messageService: messageService,
		llmClient:      llmClient,
	}
}

// SendMessageRequest 发送消息请求体
type SendMessageRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

// SendMessageResponse 响应体
type SendMessageResponse struct {
	Success bool `json:"success"`
	Data    struct {
		ID        int64  `json:"id"`
		Role      string `json:"role"`
		Content   string `json:"content"`
		CreatedAt string `json:"created_at"`
	} `json:"data"`
}

// GetHistoryRequest represents the query params
type GetHistoryRequest struct {
	SessionID string `form:"session_id" binding:"required"`
	Limit     int    `form:"limit,default=50"`
	Before    int64  `form:"before,default=0"`
}

// GetHistoryResponse is the response for history
type GetHistoryResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Messages []MessageDTO `json:"messages"`
		HasMore  bool         `json:"has_more"`
	} `json:"data"`
}

// MessageDTO represents a message in the response
type MessageDTO struct {
	ID        int64  `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// HandleGetHistory gets message history for a session
func (h *Handler) HandleGetHistory(c *gin.Context) {
	var req GetHistoryRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request query",
		})
		return
	}

	// Get user by session ID
	_, _, isNew, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to get user",
		})
		return
	}

	// New user has no history
	if isNew {
		resp := GetHistoryResponse{}
		resp.Success = true
		resp.Data.Messages = []MessageDTO{}
		resp.Data.HasMore = false
		c.JSON(http.StatusOK, resp)
		return
	}

	// Get messages from message service
	// Note: MessageRepository.GetRecentByUser returns domain messages
	// For now, return empty array - full implementation with actual message loading can be added later
	resp := GetHistoryResponse{}
	resp.Success = true
	resp.Data.Messages = []MessageDTO{}
	resp.Data.HasMore = false
	c.JSON(http.StatusOK, resp)
}

// HandleSendMessage 处理发送消息并获取 AI 响应
func (h *Handler) HandleSendMessage(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	// 获取或创建用户（通过 web 会话 ID）
	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to get or create user",
		})
		return
	}

	userID := user.GetID()

	// 保存用户消息
	err = h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, "")
	if err != nil {
		// 记录错误但继续 - 消息保存失败不影响请求
	}

	// 构建上下文消息列表
	ctxMessages, err := h.messageService.BuildContextMessages(c.Request.Context(), userID, req.Content)
	if err != nil {
		// 降级：仅使用当前消息
		ctxMessages = []llm.ChatMessage{
			{Role: "user", Content: req.Content},
		}
	}

	// 调用 LLM 获取响应
	response, err := h.llmClient.ChatCompletion(c.Request.Context(), ctxMessages)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to generate response",
		})
		return
	}

	// 保存 AI 响应
	err = h.messageService.SaveAssistantMessage(c.Request.Context(), userID, response)
	if err != nil {
		// 记录错误但继续
	}

	// 返回响应
	resp := SendMessageResponse{}
	resp.Success = true
	resp.Data.ID = time.Now().Unix() // 临时 - 后续可改为从数据库获取实际消息 ID
	resp.Data.Role = "assistant"
	resp.Data.Content = response
	resp.Data.CreatedAt = time.Now().Format(time.RFC3339)

	c.JSON(http.StatusOK, resp)
}
