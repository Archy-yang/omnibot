package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	agentpkg "omnibot/internal/service/agent"
	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/middleware"
	"omnibot/pkg/logger"
)

// AgentTaskHandler 后台 Agent 任务的 Web 接口(08 §4.5/§4.7):
//   - GET /api/v1/agent/tasks?status=completed_unreported -- 前端轮询未汇报任务
//   - POST /api/v1/agent/tasks/:id/report -- 触发主 Agent 流式汇报该任务(SSE)
type AgentTaskHandler struct {
	subAgentSvc    *agentpkg.SubAgentService
	agentService   AgentService
	registry       *agentpkg.SubAgentRegistry
	llmConfigService LLMConfigService
}

// NewAgentTaskHandler 创建任务 handler。
func NewAgentTaskHandler(
	subAgentSvc *agentpkg.SubAgentService,
	agentService AgentService,
	registry *agentpkg.SubAgentRegistry,
	llmConfigService LLMConfigService,
) *AgentTaskHandler {
	return &AgentTaskHandler{
		subAgentSvc:    subAgentSvc,
		agentService:   agentService,
		registry:       registry,
		llmConfigService: llmConfigService,
	}
}

// taskDTO 任务展示结构。
type taskDTO struct {
	ID           int64  `json:"id"`
	SubAgentType string `json:"sub_agent_type"`
	Goal         string `json:"goal"`
	Status       string `json:"status"`
	Reported     bool   `json:"reported"`
}

// HandleListTasks GET /api/v1/agent/tasks?status=completed_unreported
// 第一版仅支持 status=completed_unreported(前端轮询用)。无 status 返回全部任务。
func (h *AgentTaskHandler) HandleListTasks(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	status := c.Query("status")
	var tasks []*domainagent.AgentTask
	var err error
	if status == "completed_unreported" {
		domainTasks, e := h.subAgentSvc.GetCompletedUnreported(userID)
		if e != nil {
			logger.ErrorWithFields("list completed unreported failed", zap.Int64("user_id", userID), zap.Error(e))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询任务失败"})
			return
		}
		tasks = domainTasks
		err = nil
	} else {
		// 第一版:无 status 参数不返回全部(任务面板留后续),统一按未汇报返回
		domainTasks, e := h.subAgentSvc.GetCompletedUnreported(userID)
		if e != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询任务失败"})
			return
		}
		tasks = domainTasks
		err = nil
	}
	_ = err

	dtos := make([]taskDTO, 0, len(tasks))
	for _, t := range tasks {
		dtos = append(dtos, taskDTO{
			ID:           t.ID,
			SubAgentType: t.SubAgentType,
			Goal:         t.Goal,
			Status:       t.Status,
			Reported:     t.Reported,
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tasks": dtos}})
}

// HandleReportTask POST /api/v1/agent/tasks/:id/report (SSE)
// 触发主 Agent 流式汇报该任务。汇报不落 messages 表(不污染对话历史)。
func (h *AgentTaskHandler) HandleReportTask(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的任务 ID"})
		return
	}

	task, err := h.subAgentSvc.GetTask(taskID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "任务不存在"})
		return
	}
	// 属主校验 + 状态校验 + 未汇报校验(安全红线:用户只能汇报自己的任务)
	if task.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "无权操作此任务"})
		return
	}
	if task.Status != "completed" && task.Status != "failed" {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "任务尚未完成"})
		return
	}
	if task.Reported {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "任务已汇报"})
		return
	}

	// 选 LLM:用户自定义优先(与主对话一致,汇报是主对话语义)
	activeStreamClient := h.agentService.DefaultStreamingLLMClient()
	userConfig, hasCustomConfig, cfgErr := h.llmConfigService.GetFullConfigForUser(userID)
	if cfgErr == nil && hasCustomConfig {
		customClient := agentpkg.NewOpenAILLMClient(
			userConfig.APIKey, userConfig.BaseURL, userConfig.Model, 30*time.Second,
		)
		activeStreamClient = customClient
	}

	// 构造汇报上下文:system(回执+汇报指令) + user(虚拟触发)
	instruction := agentpkg.BuildReportInstruction(h.registry, []*domainagent.AgentTask{task})
	conversation := []map[string]interface{}{
		{"role": "system", "content": instruction},
		{"role": "user", "content": "请汇报这个子任务的结果。"},
	}

	// SSE 头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Streaming not supported"})
		return
	}

	eventCh, err := h.agentService.RunStream(c.Request.Context(), userID, conversation, activeStreamClient)
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}

	// 推 SSE(复用现有事件格式:token/thought/final/tool_call/tool_result/done/error)
	// 汇报通常一轮无工具,主要是 token -> final -> done。不落库。
	for ev := range eventCh {
		switch ev.Type {
		case agentpkg.AgentEventToken:
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: token\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventThought:
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: thought\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventFinal:
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: final\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolCall:
			data, _ := json.Marshal(map[string]string{"tool": ev.ToolName, "label": ev.ToolLabel})
			fmt.Fprintf(c.Writer, "event: tool_call\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolResult:
			data, _ := json.Marshal(map[string]string{"tool": ev.ToolName, "result": ev.ToolResult})
			fmt.Fprintf(c.Writer, "event: tool_result\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventError:
			errData, _ := json.Marshal(map[string]string{"error": ev.Error.Error()})
			fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
			flusher.Flush()
			return
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	// 汇报完标记 reported(防重复汇报)
	if err := h.subAgentSvc.MarkReported(taskID); err != nil {
		logger.ErrorWithFields("mark reported failed",
			zap.Int64("task_id", taskID), zap.Error(err))
	}
}
