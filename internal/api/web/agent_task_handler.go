package web

import (
	"context"
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
	// plans 计划续跑(34 号):Web 端"继续"按钮。为 nil 时接口报 503,不影响其他任务接口。
	plans PlanResumer
}

// PlanResumer 计划续跑窄接口(agentpkg.PlanRunner 实现)。
type PlanResumer interface {
	Resume(ctx context.Context, userID, planID int64) error
}

// SetPlanRunner 注入计划能力(装配层在 PlanRunner 就绪后调用)。
func (h *AgentTaskHandler) SetPlanRunner(p PlanResumer) { h.plans = p }

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

// taskDTO 任务列表行展示结构。
type taskDTO struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`           // 任务短名(可空,前端回落 goal 摘要)
	SubAgentType string `json:"sub_agent_type"` // 溯源标签
	Goal         string `json:"goal"`
	Status       string `json:"status"`
	Reported     bool   `json:"reported"`
	CreatedAt    string `json:"created_at"`
	FinishedAt   string `json:"finished_at,omitempty"` // completed/cancelled 的结束时间
	// 34 号合并建模:容器/子任务信息。Kind=plan 的行在任务中心折叠子步骤。
	Kind    string `json:"kind,omitempty"`
	StepSeq int    `json:"step_seq,omitempty"`
	// StepDone/StepTotal 仅 plan 行:进度 3/4。
	StepDone  int `json:"step_done,omitempty"`
	StepTotal int `json:"step_total,omitempty"`
	// Children 仅 plan 行:子步骤(前端折叠在父任务里,点开可进各步详情)
	Children []taskStepDTO `json:"children,omitempty"`
}

// taskDetailDTO 任务详情展示结构(任务中心详情页)。
type taskDetailDTO struct {
	ID           int64                `json:"id"`
	Name         string               `json:"name"`
	SubAgentType string               `json:"sub_agent_type"`
	Goal         string               `json:"goal"`
	Status       string               `json:"status"`
	Reported     bool                 `json:"reported"`
	CreatedAt    string               `json:"created_at"`
	StartedAt    string               `json:"started_at,omitempty"`
	CompletedAt  string               `json:"completed_at,omitempty"`
	Artifact     string               `json:"artifact,omitempty"`  // 结果全文(completed 时)
	ErrorMsg     string               `json:"error_msg,omitempty"` // failed 时
	Spec         domainagent.TaskSpec `json:"spec"`                // 任务合同(背景/交付物/完成标准)
	// 34 号合并建模:容器标记 + 子任务归属
	Kind     string `json:"kind,omitempty"`
	ParentID *int64 `json:"parent_id,omitempty"`
	StepSeq  int    `json:"step_seq,omitempty"`
	// Children 计划容器的子任务(仅 kind=plan 且详情页需要)
	Children []taskStepDTO `json:"children,omitempty"`
}

// taskStepDTO 计划子任务展示(详情页/列表折叠)。
type taskStepDTO struct {
	ID      int64  `json:"id"`
	StepSeq int    `json:"step_seq"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	// ArtifactHead 结果首行(不塞全文,详情点进去看)
	ArtifactHead string `json:"artifact_head,omitempty"`
}

// HandleListTasks GET /api/v1/agent/tasks
//   - status=completed_unreported:前端轮询未汇报任务(汇报链路,保持兼容)
//   - 无 status:返回该用户全部任务(倒序),任务中心列表用
func (h *AgentTaskHandler) HandleListTasks(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	status := c.Query("status")
	var dtos []taskDTO
	if status == "completed_unreported" {
		tasks, e := h.subAgentSvc.GetCompletedUnreported(userID, domainagent.SourceWeb)
		if e != nil {
			logger.ErrorWithFields("list completed unreported failed", zap.Int64("user_id", userID), zap.Error(e))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询任务失败"})
			return
		}
		for _, t := range tasks {
			dtos = append(dtos, newTaskDTO(t))
		}
	} else {
		// 任务中心全量列表:各状态含已汇报,倒序
		summaries, e := h.subAgentSvc.ListUserTasks(userID, 100)
		if e != nil {
			logger.ErrorWithFields("list tasks failed", zap.Int64("user_id", userID), zap.Error(e))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询任务失败"})
			return
		}
		for _, s := range summaries {
			dto := taskDTO{
				ID: s.ID, Name: s.Name, SubAgentType: s.SubAgent, Goal: s.Goal,
				Status: s.Status, Reported: s.Reported,
				CreatedAt: s.CreatedAt.Format("2006-01-02 15:04"),
				Kind:      s.Kind, StepSeq: s.StepSeq,
			}
			if s.FinishedAt != nil {
				dto.FinishedAt = s.FinishedAt.Format("2006-01-02 15:04")
			}
			// 计划容器补进度与子步骤(折叠在父任务里,列表显示 3/4,展开看每步)
			if s.Kind == domainagent.TaskKindPlan {
				if children, e := h.subAgentSvc.ChildrenOf(s.ID); e == nil {
					dto.StepTotal = len(children)
					for _, c := range children {
						if c.Status == domainagent.TaskStatusCompleted {
							dto.StepDone++
						}
						step := taskStepDTO{ID: c.ID, StepSeq: c.StepSeq, Name: c.Name, Status: c.Status}
						if c.Artifact != nil {
							step.ArtifactHead = domainagent.FirstLine(*c.Artifact, 80)
						}
						dto.Children = append(dto.Children, step)
					}
				}
			}
			dtos = append(dtos, dto)
		}
	}
	if dtos == nil {
		dtos = make([]taskDTO, 0)
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"tasks": dtos}})
}

// newTaskDTO 从 domain 任务构造列表行(completed_unreported 轮询路径)。
func newTaskDTO(t *domainagent.AgentTask) taskDTO {
	return taskDTO{
		ID: t.ID, Name: t.Name, SubAgentType: t.SubAgentType, Goal: t.Goal,
		Status: t.Status, Reported: t.Reported,
		CreatedAt: t.CreatedAt.Format("2006-01-02 15:04"),
		Kind:      domainagent.NormalizeKind(t.Kind), StepSeq: t.StepSeq,
	}
}

// HandleGetTaskDetail GET /api/v1/agent/tasks/:id
// 任务中心详情页:任务合同 + artifact 结果全文 + 关键时间戳。属主校验(安全红线)。
func (h *AgentTaskHandler) HandleGetTaskDetail(c *gin.Context) {
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
	if task.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "无权操作此任务"})
		return
	}

	detail := taskDetailDTO{
		ID: task.ID, Name: task.Name, SubAgentType: task.SubAgentType,
		Goal: task.Goal, Status: task.Status, Reported: task.Reported,
		CreatedAt: task.CreatedAt.Format("2006-01-02 15:04:05"),
		Spec:      task.TaskSpec,
		Kind:      domainagent.NormalizeKind(task.Kind),
		ParentID:  task.ParentTaskID, StepSeq: task.StepSeq,
	}
	// 计划容器:带上子任务(执行流水/结果在子任务详情里看)
	if task.IsPlan() {
		if children, e := h.subAgentSvc.ChildrenOf(task.ID); e == nil {
			for _, c := range children {
				step := taskStepDTO{ID: c.ID, StepSeq: c.StepSeq, Name: c.Name, Status: c.Status}
				if c.Artifact != nil {
					step.ArtifactHead = domainagent.FirstLine(*c.Artifact, 80)
				}
				detail.Children = append(detail.Children, step)
			}
		}
	}
	if task.StartedAt != nil {
		detail.StartedAt = task.StartedAt.Format("2006-01-02 15:04:05")
	}
	if task.CompletedAt != nil {
		detail.CompletedAt = task.CompletedAt.Format("2006-01-02 15:04:05")
	}
	if task.Artifact != nil {
		detail.Artifact = *task.Artifact
	}
	if task.ErrorMsg != nil {
		detail.ErrorMsg = *task.ErrorMsg
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"task": detail}})
}

// HandleResumeTask POST /api/v1/agent/tasks/:id/resume
// 仅对"暂停的计划容器"生效(34 号):任务中心"继续"按钮走后端,不必回到对话里说一句。
func (h *AgentTaskHandler) HandleResumeTask(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	taskID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || taskID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的任务 ID"})
		return
	}
	if h.plans == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "error": "计划能力未就绪"})
		return
	}
	task, err := h.subAgentSvc.GetTask(taskID)
	if err != nil || task == nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "任务不存在"})
		return
	}
	if task.UserID != userID {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "error": "无权操作此任务"})
		return
	}
	if !task.IsPlan() {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "这不是计划,单任务不支持继续"})
		return
	}
	if task.Status != domainagent.TaskStatusPaused {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "计划当前状态不可继续"})
		return
	}
	if err := h.plans.Resume(c.Request.Context(), userID, taskID); err != nil {
		switch {
		case errors.Is(err, agentpkg.ErrPlanNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "计划不存在"})
		case errors.Is(err, agentpkg.ErrPlanNotResumable):
			c.JSON(http.StatusConflict, gin.H{"success": false, "error": "计划当前状态不可继续"})
		default:
			logger.ErrorWithFields("resume plan failed",
				zap.Int64("plan_id", taskID), zap.Int64("user_id", userID), zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "继续失败"})
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"status": domainagent.TaskStatusRunning}})
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
	// 33 号:汇报归属来源渠道——Web 只汇报 web 任务,IM 任务由各自渠道推送/补报。
	// 结果本身仍可在任务中心查看(含 artifact),这里只拦"替别的渠道生成汇报"。
	if task.Source != domainagent.SourceWeb {
		c.JSON(http.StatusConflict, gin.H{"success": false, "error": "该任务的汇报归属来源渠道,请在对应渠道查看"})
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
	// system(回执+汇报指令) + 最近对话历史(锚定"用户当初为什么派这个活") + 虚拟触发 user。
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
		case agentpkg.AgentEventReasoning:
			// 深度思考增量(M5/C):与主聊天流同款累积(汇报流通常单轮,但兼容思考模型)
			if n := len(segments); n > 0 && segments[n-1].Type == "reasoning" {
				segments[n-1].Content += ev.Content
			} else {
				segments = append(segments, conversation.MessageSegment{Type: "reasoning", Content: ev.Content})
			}
			data, _ := json.Marshal(map[string]string{"content": ev.Content})
			fmt.Fprintf(c.Writer, "event: reasoning\ndata: %s\n\n", data)
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
		// report 的逻辑归属是任务发起时的 Turn(可晚于后续 Turn,§5.5)
		var reportTurnID int64
		if task.OriginTurnID != nil {
			reportTurnID = *task.OriginTurnID
		}
		if err := h.messageService.SaveReportMessage(c.Request.Context(), userID, taskID, reportTurnID, finalContent, segments, steps); err != nil {
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
