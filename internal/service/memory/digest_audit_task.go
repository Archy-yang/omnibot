package memory

// digest_audit_task.go — 沉淀留痕的任务化适配(实现 DigestAudit;2026-10 审计 #4 迁出 API 层)。
// 每轮沉淀落一条 agent_tasks(type=digest,Reported=true 静默)+ step 流,可观测不打扰。

import (
	"fmt"
	"time"

	domainagent "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	agentRepo "omnibot/internal/repository/agent"
	chatRepo "omnibot/internal/repository/chat"
)

// TaskAuditAdapter 把沉淀留痕挂到 agent_tasks/agent_steps(后台任务框架 08 号)。
type TaskAuditAdapter struct {
	TaskRepo agentRepo.AgentTaskRepository
	StepRepo chatRepo.AgentStepRepository
}

// BeginTask 开启一轮沉淀任务:创建并推进到 running,返回任务 ID。
func (a *TaskAuditAdapter) BeginTask(userID, fromID, toID int64, msgCount int) (int64, error) {
	spec := domainagent.TaskSpec{
		Goal: fmt.Sprintf("记忆沉淀:消息 #%d-#%d(%d 条)", fromID, toID, msgCount),
		Type: "digest",
	}
	t := domainagent.NewAgentTask(userID, spec, "web", "")
	t.Reported = true // 静默:digest 是系统能力,完成/失败都不打扰主 Agent
	if err := a.TaskRepo.Create(t); err != nil {
		return 0, err
	}
	if ok, err := a.TaskRepo.TransitionStatus(t.ID, domainagent.TaskStatusPending, domainagent.TaskStatusRunning, nil, nil); err != nil || !ok {
		return 0, fmt.Errorf("digest task %d pending→running 迁移失败: %v", t.ID, err)
	}
	return t.ID, nil
}

// RecordStep 记录一步工具/LLM 调用。
func (a *TaskAuditAdapter) RecordStep(taskID, userID int64, seq int, kind, tool, request, response, status string, durationMs int64) error {
	step := &conversation.AgentStep{
		UserID: userID, TaskID: &taskID, Seq: seq,
		Kind: kind, Status: status, DurationMs: durationMs,
		Tool: tool, Request: request, Response: response,
		CreatedAt: time.Now(),
	}
	return a.StepRepo.CreateBatch([]*conversation.AgentStep{step})
}

// EndTask 收尾:CAS running→终态;false=状态被并发改动(异常),如实报错。
func (a *TaskAuditAdapter) EndTask(taskID int64, status, artifact, errMsg string) error {
	var artifactPtr, errMsgPtr *string
	if artifact != "" {
		artifactPtr = &artifact
	}
	if errMsg != "" {
		errMsgPtr = &errMsg
	}
	ok, err := a.TaskRepo.TransitionStatus(taskID, domainagent.TaskStatusRunning, status, artifactPtr, errMsgPtr)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("digest task %d 状态迁移到 %s 失败(当前非 running)", taskID, status)
	}
	return nil
}
