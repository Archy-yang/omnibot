// Package web implements the Web chat API handlers
package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"omnibot/internal/client/llm"
	memorydomain "omnibot/internal/domain/memory"
	domainuser "omnibot/internal/domain/user"
	memorysvc "omnibot/internal/service/memory"
	userLLM "omnibot/internal/service/user"

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
	StreamChatCompletion(ctx context.Context, messages []llm.ChatMessage) (<-chan llm.StreamChunk, error)
}

// LLMConfigService LLM 配置服务接口
type LLMConfigService interface {
	GetConfigView(userID int64) (*userLLM.LLMConfigView, error)
	UpdateFullConfig(userID int64, req userLLM.UpdateConfigRequest) error
	ClearConfig(userID int64) error
	GetFullConfigForUser(userID int64) (*userLLM.FullLLMConfig, bool, error)
	ListProviderOptions() []userLLM.ProviderOption
}

// MemoryService 长期记忆服务接口
type MemoryService interface {
	Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error)
	List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error)
	Clear(ctx context.Context, userID int64) error
	Delete(ctx context.Context, userID int64, memoryID int64) (bool, error)
	Update(ctx context.Context, userID int64, memoryID int64, content string) (*memorydomain.Memory, error)
}

// Handler Web 聊天 API 处理器
type Handler struct {
	userService      UserService
	messageService   MessageService
	llmClient        LLMClient
	llmConfigService LLMConfigService
	memoryService    MemoryService
}

// NewHandler 创建 Web 聊天处理器
func NewHandler(
	userService UserService,
	messageService MessageService,
	llmClient LLMClient,
	llmConfigService LLMConfigService,
	memoryService MemoryService,
) *Handler {
	return &Handler{
		userService:      userService,
		messageService:   messageService,
		llmClient:        llmClient,
		llmConfigService: llmConfigService,
		memoryService:    memoryService,
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

	// 选择 LLM 客户端：优先使用用户自定义配置
	activeClient := h.llmClient
	userConfig, hasCustomConfig, err := h.llmConfigService.GetFullConfigForUser(userID)
	if err == nil && hasCustomConfig {
		// 使用用户自定义配置创建 LLM 客户端
		customConfig := llm.UserConfig{
			Provider: userConfig.Provider,
			APIKey:   userConfig.APIKey,
			BaseURL:  userConfig.BaseURL,
			Model:    userConfig.Model,
		}
		customClient, err := llm.NewClientFromUserConfig(customConfig)
		if err == nil {
			activeClient = customClient
		}
		// 失败则静默降级使用系统默认客户端
	}

	// 调用 LLM 获取响应
	response, err := activeClient.ChatCompletion(c.Request.Context(), ctxMessages)
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

// HandleSendMessageStream SSE 流式对话
func (h *Handler) HandleSendMessageStream(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "Invalid request body",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to get or create user",
		})
		return
	}

	userID := user.GetID()

	h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, "")

	ctxMessages, err := h.messageService.BuildContextMessages(c.Request.Context(), userID, req.Content)
	if err != nil {
		ctxMessages = []llm.ChatMessage{
			{Role: "user", Content: req.Content},
		}
	}

	activeClient := h.llmClient
	userConfig, hasCustomConfig, err := h.llmConfigService.GetFullConfigForUser(userID)
	if err == nil && hasCustomConfig {
		customConfig := llm.UserConfig{
			Provider: userConfig.Provider,
			APIKey:   userConfig.APIKey,
			BaseURL:  userConfig.BaseURL,
			Model:    userConfig.Model,
		}
		customClient, err := llm.NewClientFromUserConfig(customConfig)
		if err == nil {
			activeClient = customClient
		}
	}

	ch, err := activeClient.StreamChatCompletion(c.Request.Context(), ctxMessages)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to generate response",
		})
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Streaming not supported",
		})
		return
	}

	var fullContent strings.Builder
	for chunk := range ch {
		if chunk.Error != nil {
			data, _ := json.Marshal(map[string]string{"error": chunk.Error.Error()})
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
			flusher.Flush()
			return
		}
		if chunk.Done {
			break
		}
		fullContent.WriteString(chunk.Content)
		data, _ := json.Marshal(map[string]string{"content": chunk.Content})
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	h.messageService.SaveAssistantMessage(c.Request.Context(), userID, fullContent.String())
}

// ========== 长期记忆接口 ==========

type MemoryDTO struct {
	ID        int64  `json:"id"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type GetMemoriesRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

type GetMemoriesResponse struct {
	Memories []MemoryDTO `json:"memories"`
}

type CreateMemoryRequest struct {
	SessionID string `json:"session_id" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

type CreateMemoryResponse struct {
	Message string    `json:"message"`
	Memory  MemoryDTO `json:"memory"`
}

type ClearMemoriesRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

type ClearMemoriesResponse struct {
	Message string `json:"message"`
}

type DeleteMemoryQueryRequest struct {
	SessionID string `form:"session_id" binding:"required"`
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
	SessionID string `json:"session_id" binding:"required"`
	Content   string `json:"content" binding:"required"`
}

type UpdateMemoryResponse struct {
	Message string    `json:"message"`
	Memory  MemoryDTO `json:"memory"`
}

func toMemoryDTO(memory *memorydomain.Memory) MemoryDTO {
	return MemoryDTO{
		ID:        memory.ID,
		Content:   memory.Content,
		CreatedAt: memory.CreatedAt.Format(time.RFC3339),
	}
}

// HandleGetMemories 获取用户的全部长期记忆
func (h *Handler) HandleGetMemories(c *gin.Context) {
	var req GetMemoriesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	memories, err := h.memoryService.List(c.Request.Context(), user.ID)
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

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	memory, err := h.memoryService.Remember(c.Request.Context(), user.ID, req.Content)
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

// HandleClearMemories 清空用户的全部长期记忆
func (h *Handler) HandleClearMemories(c *gin.Context) {
	var req ClearMemoriesRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	if err := h.memoryService.Clear(c.Request.Context(), user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": ClearMemoriesResponse{
			Message: "已清空你的全部长期记忆。",
		},
	})
}

// HandleDeleteMemory 删除单条长期记忆
func (h *Handler) HandleDeleteMemory(c *gin.Context) {
	var queryReq DeleteMemoryQueryRequest
	if err := c.ShouldBindQuery(&queryReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	var uriReq DeleteMemoryURIRequest
	if err := c.ShouldBindUri(&uriReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "无效的记忆 ID。",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", queryReq.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	deleted, err := h.memoryService.Delete(c.Request.Context(), user.ID, uriReq.MemoryID)
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

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "服务暂时不可用，请稍后再试。",
		})
		return
	}

	memory, err := h.memoryService.Update(c.Request.Context(), user.ID, uriReq.MemoryID, req.Content)
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

// ========== LLM 配置接口 ==========

// LLMProviderOptionDTO 服务商预设选项 DTO
type LLMProviderOptionDTO struct {
	Value          string `json:"value"`
	Label          string `json:"label"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	DefaultBaseURL string `json:"default_base_url,omitempty"`
	DefaultModel   string `json:"default_model,omitempty"`
	Description    string `json:"description,omitempty"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

// GetLLMProvidersResponse 提供商列表响应
type GetLLMProvidersResponse struct {
	Providers []LLMProviderOptionDTO `json:"providers"`
}

func toProviderOptionDTO(opt userLLM.ProviderOption) LLMProviderOptionDTO {
	return LLMProviderOptionDTO{
		Value:          opt.Value,
		Label:          opt.Label,
		Mode:           opt.Mode,
		Status:         opt.Status,
		DefaultBaseURL: opt.DefaultBaseURL,
		DefaultModel:   opt.DefaultModel,
		Description:    opt.Description,
		DisabledReason: opt.DisabledReason,
	}
}

// HandleGetLLMProviders 获取所有可用的 LLM 提供商选项
func (h *Handler) HandleGetLLMProviders(c *gin.Context) {
	options := h.llmConfigService.ListProviderOptions()

	items := make([]LLMProviderOptionDTO, len(options))
	for i, opt := range options {
		items[i] = toProviderOptionDTO(opt)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": GetLLMProvidersResponse{
			Providers: items,
		},
	})
}

// GetLLMConfigRequest 获取配置请求
type GetLLMConfigRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

// GetLLMConfigResponse 获取配置响应
type GetLLMConfigResponse struct {
	HasConfig   bool    `json:"has_config"`
	APIKeyMask string `json:"api_key_masked"`
	BaseURL     string `json:"base_url"`
	Model       string `json:"model"`
	Provider    string `json:"provider"`
	StatusText  string `json:"status_text"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

// HandleGetLLMConfig 获取用户 LLM 配置
func (h *Handler) HandleGetLLMConfig(c *gin.Context) {
	var req GetLLMConfigRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "获取用户信息失败",
		})
		return
	}

	configView, err := h.llmConfigService.GetConfigView(user.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "获取配置失败",
		})
		return
	}

	resp := GetLLMConfigResponse{
		HasConfig:   configView.HasConfig,
		APIKeyMask: configView.APIKeyMasked,
		BaseURL:     configView.BaseURL,
		Model:       configView.Model,
		Provider:    configView.Provider,
		StatusText:  configView.StatusText,
		Temperature: configView.Temperature,
		MaxTokens:   configView.MaxTokens,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}

// UpdateLLMConfigRequest 更新配置请求
type UpdateLLMConfigRequest struct {
	SessionID   string  `json:"session_id" binding:"required"`
	Provider    string `json:"provider" binding:"required"`
	APIKey      string `json:"api_key"`
	BaseURL     string `json:"base_url"`
	Model       string `json:"model" binding:"required"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
}

// HandleUpdateLLMConfig 更新用户 LLM 配置
func (h *Handler) HandleUpdateLLMConfig(c *gin.Context) {
	var req UpdateLLMConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "请求参数错误: " + err.Error(),
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "获取用户信息失败",
		})
		return
	}

	updateReq := userLLM.UpdateConfigRequest{
		Provider:    req.Provider,
		APIKey:      req.APIKey,
		BaseURL:     req.BaseURL,
		Model:       req.Model,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}

	if err := h.llmConfigService.UpdateFullConfig(user.ID, updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置保存成功",
	})
}

// DeleteLLMConfigRequest 删除配置请求
type DeleteLLMConfigRequest struct {
	SessionID string `form:"session_id" binding:"required"`
}

// HandleDeleteLLMConfig 删除用户 LLM 配置
func (h *Handler) HandleDeleteLLMConfig(c *gin.Context) {
	var req DeleteLLMConfigRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "缺少 session_id 参数",
		})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "获取用户信息失败",
		})
		return
	}

	if err := h.llmConfigService.ClearConfig(user.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "清除配置失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置已清除",
	})
}
