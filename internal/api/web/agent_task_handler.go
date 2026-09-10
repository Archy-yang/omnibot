package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	domainagent "omnibot/internal/domain/agent"
	conversation "omnibot/internal/domain/conversation"
	"omnibot/internal/middleware"
	agentpkg "omnibot/internal/service/agent"
	"omnibot/pkg/logger"
)

// AgentTaskHandler 后台 Agent 任务的 Web 接口(08 §4.5/§4.7):
//   - GET /api/v1/agent/tasks?status=completed_unreported -- 前端轮询未汇报任务
//   - POST /api/v1/agent/tasks/:id/report -- 触发主 Agent 流式汇报该任务(SSE)
type AgentTaskHandler struct {
	subAgentSvc      *agentpkg.SubAgentService
	agentService     AgentService
	llmConfigService LLMConfigService
	messageService   MessageService // 落库汇报消息(Kind=report),刷新后历史仍能还原
}

// NewAgentTaskHandler 创建任务 handler。去角色后无 registry。
func NewAgentTaskHandler(
	subAgentSvc *agentpkg.SubAgentService,
	agentService AgentService,
	llmConfigService LLMConfigService,
	messageService MessageService,
) *AgentTaskHandler {
	return &AgentTaskHandler{
		subAgentSvc:      subAgentSvc,
		agentService:     agentService,
		llmConfigService: llmConfigService,
		messageService:   messageService,
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
// 触发主 Agent 流式汇报该任务,流式推给前端的同时落库为 Kind=report 消息(关联 task_id),
// 使刷新后历史仍能还原汇报(不再只是前端内存里一闪而过)。
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

	// 构造汇报上下文(汇报锚定,§3.4 修订):
	// system(回执+汇报指令,含验收自查) + 最近对话历史(锚定"用户当初为什么派这个活") + 虚拟触发 user。
	// 历史经 BuildContextMessages(含长期记忆注入),原始问题通常在窗口内,汇报口吻也能延续对话。
	instruction := agentpkg.BuildReportInstruction([]*domainagent.AgentTask{task}, true)
	reportConversation := []map[string]interface{}{
		{"role": "system", "content": instruction},
	}
	// 注:局部变量命名为 reportConversation,避免遮蔽 conversation 包导入(下方累积 segments/steps 要用)。
	trigger := "请汇报这个子任务的结果。"
	if ctxMsgs, err := h.messageService.BuildContextMessages(c.Request.Context(), userID, trigger); err == nil {
		reportConversation = append(reportConversation, toAgentMessages(ctxMsgs)...)
	} else {
		// 历史拉取失败降级为纯回执汇报(不阻断)
		reportConversation = append(reportConversation, map[string]interface{}{"role": "user", "content": trigger})
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

	eventCh, err := h.agentService.RunStream(c.Request.Context(), userID, reportConversation, activeStreamClient)
	if err != nil {
		errData, _ := json.Marshal(map[string]string{"error": err.Error()})
		fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
		flusher.Flush()
		return
	}

	// 累积汇报消息落库(Kind=report):finalContent(最终回复)+ segments(思考/工具展示)+ steps(运行链路)。
	// 与 HandleSendMessageAgentStream 同款累积逻辑;汇报通常单轮无工具,但仍兼容多轮/工具。
	var finalContent string
	var doneFallback string
	var segments []conversation.MessageSegment
	var steps []*conversation.AgentStep
	seq := 0
	for ev := range eventCh {
		switch ev.Type {
		case agentpkg.AgentEventToken:
			if n := len(segments); n > 0 && segments[n-1].Type == "text" {
				segments[n-1].Content += ev.Content
			} else {
				segments = append(segments, conversation.MessageSegment{Type: "text", Role: "final", Content: ev.Content})
			}
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: token\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventThought:
			for i := len(segments) - 1; i >= 0; i-- {
				if segments[i].Type == "text" {
					segments[i].Role = "thought"
					break
				}
			}
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: thought\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventFinal:
			finalContent = ev.Content
			for i := len(segments) - 1; i >= 0; i-- {
				if segments[i].Type == "text" {
					segments[i].Role = "final"
					break
				}
			}
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: final\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolCall:
			segments = append(segments, conversation.MessageSegment{Type: "tool", Tool: ev.ToolName, Label: ev.ToolLabel})
			data, _ := json.Marshal(map[string]string{"tool": ev.ToolName, "label": ev.ToolLabel})
			fmt.Fprintf(c.Writer, "event: tool_call\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventToolResult:
			sanitized := sanitizeToolResult(ev.ToolResult)
			for i := len(segments) - 1; i >= 0; i-- {
				if segments[i].Type == "tool" && segments[i].Result == "" {
					segments[i].Result = sanitized
					break
				}
			}
			toolStep := conversation.NewToolStep(userID, ev.ToolName, ev.ToolArguments, ev.ToolResult, ev.StepStatus, ev.StepDurationMs)
			toolStep.Seq = seq
			seq++
			steps = append(steps, toolStep)
			data, _ := json.Marshal(map[string]string{"tool": ev.ToolName, "result": sanitized})
			fmt.Fprintf(c.Writer, "event: tool_result\ndata: %s\n\n", data)
			flusher.Flush()
		case agentpkg.AgentEventLLMCall:
			llmStep := conversation.NewLLMStep(userID, ev.LLMRequest, ev.LLMResponse, "", ev.StepStatus, ev.StepDurationMs)
			llmStep.Seq = seq
			seq++
			steps = append(steps, llmStep)
		case agentpkg.AgentEventDone:
			doneFallback = ev.Content
		case agentpkg.AgentEventError:
			errData, _ := json.Marshal(map[string]string{"error": ev.Error.Error()})
			fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errData)
			flusher.Flush()
			return
		}
	}

	fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
	flusher.Flush()

	if finalContent == "" {
		finalContent = doneFallback
	}

	// 落库汇报消息(Kind=report,关联 task_id):刷新后历史仍能还原汇报。
	if finalContent != "" {
		if err := h.messageService.SaveReportMessage(c.Request.Context(), userID, taskID, finalContent, segments, steps); err != nil {
			logger.ErrorWithFields("save report message failed",
				zap.Int64("task_id", taskID), zap.Error(err))
		}
	}

	// 汇报完标记 reported(防重复汇报)
	if err := h.subAgentSvc.MarkReported(taskID); err != nil {
		logger.ErrorWithFields("mark reported failed",
			zap.Int64("task_id", taskID), zap.Error(err))
	}
}

// stepDTO 子 Agent 执行步骤的展示结构(供排查执行过程)。
type stepDTO struct {
	Seq        int    `json:"seq"`
	Kind       string `json:"kind"`            // llm_call / tool_call
	Tool       string `json:"tool,omitempty"`  // tool_call 用:工具名
	Model      string `json:"model,omitempty"` // llm_call 用:模型名
	Status     string `json:"status"`          // success / error / not_found
	DurationMs int64  `json:"duration_ms"`
	Request    string `json:"request"`  // llm: messages JSON;tool: arguments JSON
	Response   string `json:"response"` // llm: {content,tool_calls} JSON;tool: 原始结果
	CreatedAt  string `json:"created_at"`
}

// HandleListTaskSteps GET /api/v1/agent/tasks/:id/steps
// 返回某子 Agent 任务的执行步骤链(LLM调用 + 工具调用),按 seq 正序,供排查/展示执行过程。
// 属主校验:只能查自己的任务。
func (h *AgentTaskHandler) HandleListTaskSteps(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的任务 ID"})
		return
	}

	steps, err := h.subAgentSvc.ListTaskSteps(taskID, userID)
	if err != nil {
		if errors.Is(err, agentpkg.ErrTaskNotOwned) {
			c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "无权操作此任务"})
			return
		}
		// not found 或其他错误,与 HandleReportTask 一致统一 404
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "任务不存在"})
		return
	}

	dtos := make([]stepDTO, 0, len(steps))
	for _, s := range steps {
		dtos = append(dtos, stepDTO{
			Seq:        s.Seq,
			Kind:       s.Kind,
			Tool:       s.Tool,
			Model:      s.Model,
			Status:     s.Status,
			DurationMs: s.DurationMs,
			Request:    s.Request,
			Response:   s.Response,
			CreatedAt:  s.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"steps": dtos}})
}
