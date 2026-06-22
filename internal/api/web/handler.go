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
	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	domainuser "omnibot/internal/domain/user"
	agentpkg "omnibot/internal/service/agent"
	memorysvc "omnibot/internal/service/memory"
	userLLM "omnibot/internal/service/user"
	"omnibot/pkg/logger"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
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
	SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
	ListByUser(ctx context.Context, userID int64, limit int, before int64) ([]*conversation.Message, error)
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

// AgentService Agent 服务接口
type AgentService interface {
	Run(ctx context.Context, userID int64, conversation []map[string]interface{}, customLLMClient ...agentpkg.LLMClient) (*agentpkg.AgentResult, error)
	RunStream(ctx context.Context, userID int64, conversation []map[string]interface{}, customStreamClient ...agentpkg.StreamingLLMClient) (<-chan agentpkg.AgentEvent, error)
	DefaultLLMClient() agentpkg.LLMClient
	DefaultStreamingLLMClient() agentpkg.StreamingLLMClient
}

// Handler Web 聊天 API 处理器
type Handler struct {
	userService      UserService
	messageService   MessageService
	llmClient        LLMClient
	llmConfigService LLMConfigService
	memoryService    MemoryService
	agentService     AgentService
}

// NewHandler 创建 Web 聊天处理器
func NewHandler(
	userService UserService,
	messageService MessageService,
	llmClient LLMClient,
	llmConfigService LLMConfigService,
	memoryService MemoryService,
	agentService AgentService,
) *Handler {
	return &Handler{
		userService:      userService,
		messageService:   messageService,
		llmClient:        llmClient,
		llmConfigService: llmConfigService,
		memoryService:    memoryService,
		agentService:     agentService,
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
	ID        int64                          `json:"id"`
	Role      string                         `json:"role"`
	Content   string                         `json:"content"`
	Segments  []conversation.MessageSegment  `json:"segments,omitempty"` // v1.5.4：Agent 思考过程片段，无则省略
	CreatedAt string                         `json:"created_at"`
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

	// 获取或创建用户
	user, _, isNew, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to get user",
		})
		return
	}

	// 新用户没有历史，直接返回空
	if isNew {
		resp := GetHistoryResponse{}
		resp.Success = true
		resp.Data.Messages = []MessageDTO{}
		resp.Data.HasMore = false
		c.JSON(http.StatusOK, resp)
		return
	}

	// 多取一条用于判断是否还有更多历史，前端按需翻页
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	fetchLimit := limit + 1

	messages, err := h.messageService.ListByUser(c.Request.Context(), user.GetID(), fetchLimit, req.Before)
	if err != nil {
		logger.ErrorWithFields("Failed to load chat history",
			zap.Int64("user_id", user.GetID()),
			zap.Error(err),
		)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "Failed to load chat history",
		})
		return
	}

	hasMore := len(messages) > limit
	if hasMore {
		// messages 是正序（旧→新），多取的那条是「更旧」的，位于数组头部，去掉它
		messages = messages[len(messages)-limit:]
	}

	items := make([]MessageDTO, 0, len(messages))
	for _, msg := range messages {
		items = append(items, MessageDTO{
			ID:        msg.ID,
			Role:      msg.Role,
			Content:   msg.Content,
			Segments:  msg.Segments,
			CreatedAt: msg.CreatedAt.Format(time.RFC3339),
		})
	}

	resp := GetHistoryResponse{}
	resp.Success = true
	resp.Data.Messages = items
	resp.Data.HasMore = hasMore
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
	if err := h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, ""); err != nil {
		logger.ErrorWithFields("Failed to save user message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
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
	if err := h.messageService.SaveAssistantMessage(c.Request.Context(), userID, response); err != nil {
		logger.ErrorWithFields("Failed to save assistant message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
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

	if err := h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, ""); err != nil {
		logger.ErrorWithFields("Failed to save user message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

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

	if err := h.messageService.SaveAssistantMessage(c.Request.Context(), userID, fullContent.String()); err != nil {
		logger.ErrorWithFields("Failed to save assistant message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}
}

// HandleSendMessageAgent 处理 Agent 消息
func (h *Handler) HandleSendMessageAgent(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request body"})
		return
	}
	if h.agentService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Agent service unavailable"})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get or create user"})
		return
	}
	userID := user.GetID()

	if err := h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, ""); err != nil {
		logger.ErrorWithFields("Failed to save user message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}
	ctxMessages, err := h.messageService.BuildContextMessages(c.Request.Context(), userID, req.Content)
	if err != nil {
		ctxMessages = []llm.ChatMessage{{Role: "user", Content: req.Content}}
	}

	// 选择 LLM 客户端：优先使用用户自定义配置，和普通聊天逻辑保持一致
	activeLLMClient := h.agentService.DefaultLLMClient()
	userConfig, hasCustomConfig, err := h.llmConfigService.GetFullConfigForUser(userID)
	if err == nil && hasCustomConfig {
		// 使用用户自定义配置创建 Agent LLM 客户端，所有服务商均兼容 OpenAI 协议
		timeout := 30 * time.Second
		customAgentClient := agentpkg.NewOpenAILLMClient(
			userConfig.APIKey,
			userConfig.BaseURL,
			userConfig.Model,
			timeout,
		)
		activeLLMClient = customAgentClient
	}

	result, err := h.agentService.Run(c.Request.Context(), userID, toAgentMessages(ctxMessages), activeLLMClient)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to generate response"})
		return
	}

	if err := h.messageService.SaveAssistantMessage(c.Request.Context(), userID, result.FinalResponse); err != nil {
		logger.ErrorWithFields("Failed to save assistant message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}

	resp := SendMessageResponse{}
	resp.Success = true
	resp.Data.ID = time.Now().Unix()
	resp.Data.Role = "assistant"
	resp.Data.Content = result.FinalResponse
	resp.Data.CreatedAt = time.Now().Format(time.RFC3339)
	c.JSON(http.StatusOK, resp)
}

// sanitizeToolResult 对工具结果脱敏后再发给前端。
// 工具执行成功的结果（时间、计算值、RSS 内容等）是用户自己的查询，原样返回；
// 执行失败的结果由 agent 层以固定前缀产生（见 agent.go），可能携带内部细节
// （IP、连接错误、堆栈），统一替换为友好文案，避免泄露内部实现（安全红线）。
func sanitizeToolResult(result string) string {
	if strings.HasPrefix(result, "工具执行错误:") ||
		strings.HasPrefix(result, "错误：工具 ") {
		return "工具执行失败"
	}
	return result
}

// HandleSendMessageAgentStream SSE 流式 Agent 对话。
// 这是 v1.5.2 重构后的真流式实现：边推理边吐 token，工具调用以独立事件先于结果推送。
//
// SSE 协议：
//
//	event: token       data: {"content": "..."}    -- LLM 文本 token 增量
//	event: tool_call   data: {"tool": "...", "label": "..."}  -- 工具调用开始
//	event: tool_result data: {"tool": "...", "result": "..."} -- 工具结果（错误已脱敏）
//	event: error       data: {"error": "..."}      -- 错误，流即将关闭
//	(默认 event)       data: [DONE]                -- 完成标记
func (h *Handler) HandleSendMessageAgentStream(c *gin.Context) {
	var req SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request body"})
		return
	}
	if h.agentService == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Agent service unavailable"})
		return
	}

	user, _, _, err := h.userService.GetOrCreateByChannel("web", req.SessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to get or create user"})
		return
	}
	userID := user.GetID()

	if err := h.messageService.SaveUserMessage(c.Request.Context(), userID, req.Content, ""); err != nil {
		logger.ErrorWithFields("Failed to save user message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}
	ctxMessages, err := h.messageService.BuildContextMessages(c.Request.Context(), userID, req.Content)
	if err != nil {
		ctxMessages = []llm.ChatMessage{{Role: "user", Content: req.Content}}
	}

	// 选择流式 LLM 客户端：优先使用用户自定义配置（OpenAI 兼容协议）
	activeStreamClient := h.agentService.DefaultStreamingLLMClient()
	userConfig, hasCustomConfig, err := h.llmConfigService.GetFullConfigForUser(userID)
	if err == nil && hasCustomConfig {
		timeout := 30 * time.Second
		customAgentClient := agentpkg.NewOpenAILLMClient(
			userConfig.APIKey,
			userConfig.BaseURL,
			userConfig.Model,
			timeout,
		)
		activeStreamClient = customAgentClient
	}

	// 先打开 SSE 头部和 flusher，再调 RunStream，避免错误时已经写过 header 还想 c.JSON
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Streaming not supported"})
		return
	}

	eventCh, err := h.agentService.RunStream(c.Request.Context(), userID, toAgentMessages(ctxMessages), activeStreamClient)
	if err != nil {
		// 流尚未开始（如 streaming client 未配置），按 SSE error 事件返回，前端能感知
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}

	// finalContent 累积所有 AgentEventToken 的 Content（也吸收 AgentEventDone 的兜底文本，
	// 用于在结束后持久化为 assistant 消息）。
	// segments 按和前端 store 相同的时序逻辑累积（text/tool 交错），用于历史持久化（v1.5.4）。
	var finalContent string
	var segments []conversation.MessageSegment
	// steps 累积该轮的 Agent 运行步骤链（LLM 调用 + 工具调用），落 agent_steps 表（v1.5.5）。
	// seq 是链内顺序，stepModel 用于给 llm_call 步骤标注模型名（自定义配置时已知）。
	var steps []*conversation.AgentStep
	seq := 0
	stepModel := ""
	if hasCustomConfig && userConfig != nil {
		stepModel = userConfig.Model
	}
	for ev := range eventCh {
		switch ev.Type {
		case agentpkg.AgentEventToken:
			finalContent += ev.Content
			// 末尾是 text 段就追加，否则新建 text 段（与前端 store onChunk 一致）
			if n := len(segments); n > 0 && segments[n-1].Type == "text" {
				segments[n-1].Content += ev.Content
			} else {
				segments = append(segments, conversation.MessageSegment{Type: "text", Content: ev.Content})
			}
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: token\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolCall:
			// push tool 段（Result 待 ToolResult 回填），自然封口上一段文本
			segments = append(segments, conversation.MessageSegment{
				Type:  "tool",
				Tool:  ev.ToolName,
				Label: ev.ToolLabel,
			})
			data, _ := json.Marshal(map[string]string{
				"tool":  ev.ToolName,
				"label": ev.ToolLabel,
			})
			fmt.Fprintf(c.Writer, "event: tool_call\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolResult:
			// v1.5.3：向前端暴露工具结果，供「点击思考条展开看详情」。
			// 安全红线：执行失败的结果可能含内部细节（IP、堆栈、连接错误），
			// 统一脱敏为友好文案，不透传原始 error。SSE 推送和展示共用同一脱敏值。
			sanitized := sanitizeToolResult(ev.ToolResult)
			// 回填最后一个 Result 为空的 tool 段（与前端 store onToolResult 一致）
			for i := len(segments) - 1; i >= 0; i-- {
				if segments[i].Type == "tool" && segments[i].Result == "" {
					segments[i].Result = sanitized
					break
				}
			}
			// v1.5.5：append 一个 tool_call 步骤。Response 用原始未脱敏值（含真实错误），
			// 供记录/分析；展示用的 segment.result 仍是上面脱敏后的值。MessageID 在 service 层 stamp。
			toolStep := conversation.NewToolStep(
				userID, ev.ToolName, ev.ToolArguments, ev.ToolResult, ev.StepStatus, ev.StepDurationMs,
			)
			toolStep.Seq = seq
			seq++
			steps = append(steps, toolStep)
			data, _ := json.Marshal(map[string]string{
				"tool":   ev.ToolName,
				"result": sanitized,
			})
			fmt.Fprintf(c.Writer, "event: tool_result\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventLLMCall:
			// v1.5.5：append 一个 llm_call 步骤。不推给前端，仅落 agent_steps。
			llmStep := conversation.NewLLMStep(
				userID, ev.LLMRequest, ev.LLMResponse, stepModel, ev.StepStatus, ev.StepDurationMs,
			)
			llmStep.Seq = seq
			seq++
			steps = append(steps, llmStep)
		case agentpkg.AgentEventDone:
			// Done 携带的 Content 在常规 ReAct 路径下为「累计 token 拼接结果」，
			// 等价于 finalContent；超时/超步数兜底情况下是固定提示语。
			// 优先使用我们累计的 finalContent，若为空则回落到 ev.Content。
			if finalContent == "" {
				finalContent = ev.Content
			}
		case agentpkg.AgentEventError:
			errData, _ := json.Marshal(map[string]string{"error": ev.Error.Error()})
			fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
			flusher.Flush()
			return // 错误后不再推 [DONE]，前端按错误处理
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	// 落库：带 segments 的助手消息（v1.5.4），刷新后历史能还原完整思考过程。
	// 纯文本提问 segments 只有一个 text 段，对历史展示也无害。
	if err := h.messageService.SaveAssistantMessageWithSegments(c.Request.Context(), userID, finalContent, segments, steps); err != nil {
		logger.ErrorWithFields("Failed to save assistant message",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
	}
}

func toAgentMessages(messages []llm.ChatMessage) []map[string]interface{} {
	items := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		items = append(items, map[string]interface{}{"role": msg.Role, "content": msg.Content})
	}
	return items
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
	APIKeyMask  string  `json:"api_key_masked"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	StatusText  string  `json:"status_text"`
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
		APIKeyMask:  configView.APIKeyMasked,
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
	Provider    string  `json:"provider" binding:"required"`
	APIKey      string  `json:"api_key"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model" binding:"required"`
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
