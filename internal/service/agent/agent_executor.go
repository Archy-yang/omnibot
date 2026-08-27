package agent

import (
	"context"
	"fmt"

	domainagent "omnibot/internal/domain/agent"
)

// AgentExecutor Agent 执行器抽象(10-规划 §6 预留)。
//
// 把"如何执行一个 Agent 任务"抽象成统一接口,使主 Agent 不感知执行者是:
//   - 本地自研子 Agent(LocalAgentExecutor,包装现有 SubAgentService)
//   - 本地 Eino Agent(未来 EinoAgentExecutor)
//   - 远程 A2A Agent(未来 A2AAgentExecutor)
//
// 当前只实现 LocalAgentExecutor。Eino/A2A Adapter 等真实需求时再加,
// 不先写空 Adapter(避免为想象需求建框架)。
//
// 注:本接口为预留扩展点,生产路径仍直接用 SubAgentService(它已覆盖 query/update/cancel)。
// 接入外部 Agent 时,主 Agent 经此接口统一调用,届时把 SubAgentService 的调用迁过来。
type AgentExecutor interface {
	// AgentID 执行器标识(如 "local"、"eino"、"a2a")。
	AgentID() string
	// Capabilities 返回该执行器的能力(支持取消/流式/输入等)。预留,当前可返回 nil。
	Capabilities(ctx context.Context) (*AgentCapabilities, error)
	// Submit 提交任务,返回任务回执(含 taskID)。异步,不阻塞。spec 携带任务合同(含 persona_hint, 无角色)。
	Submit(ctx context.Context, userID int64, spec domainagent.TaskSpec) (*TaskReceipt, error)
	// Send 向运行中任务发消息(补充输入/update)。
	Send(ctx context.Context, taskID int64, msg AgentMessage) error
	// Cancel 取消任务。
	Cancel(ctx context.Context, taskID int64) error
	// Status 查任务状态。
	Status(ctx context.Context, taskID int64) (*AgentTaskStatus, error)
}

// AgentCapabilities 执行器能力描述(预留,未来 Agent Registry 用)。
type AgentCapabilities struct {
	SupportsStreaming         bool
	SupportsPushNotifications bool
	SupportsCancellation      bool
	SupportsInputRequired     bool
}

// TaskReceipt 提交任务后的回执。
type TaskReceipt struct {
	TaskID  int64  `json:"task_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// AgentMessage 父->子消息(补充输入)。
type AgentMessage struct {
	Role  string        `json:"role"`  // "parent_agent" / "user"
	Parts []MessagePart `json:"parts"` // 消息片段(文本/数据)
}

// MessagePart 消息片段(预留多模态:文本/数据/文件引用)。
type MessagePart struct {
	Type string      `json:"type"` // "text" / "data"
	Text string      `json:"text,omitempty"`
	Data interface{} `json:"data,omitempty"`
}

// AgentTaskStatus 任务状态快照(供 Status 接口返回)。
type AgentTaskStatus struct {
	TaskID int64  `json:"task_id"`
	Status string `json:"status"`
	Goal   string `json:"goal"`
}

// LocalAgentExecutor 本地自研子 Agent 执行器(包装 SubAgentService)。
// 实现 AgentExecutor 接口。当前为预留占位,生产路径仍直接用 SubAgentService;
// 接入外部 Agent 时把调用迁到此接口。
type LocalAgentExecutor struct {
	svc *SubAgentService
}

// NewLocalAgentExecutor 创建本地执行器。
func NewLocalAgentExecutor(svc *SubAgentService) *LocalAgentExecutor {
	return &LocalAgentExecutor{svc: svc}
}

func (e *LocalAgentExecutor) AgentID() string { return "local" }

func (e *LocalAgentExecutor) Capabilities(ctx context.Context) (*AgentCapabilities, error) {
	return &AgentCapabilities{
		SupportsCancellation:  true,
		SupportsInputRequired: true,
	}, nil
}

func (e *LocalAgentExecutor) Submit(ctx context.Context, userID int64, spec domainagent.TaskSpec) (*TaskReceipt, error) {
	// source/notifyTarget 从 ctx 取(handler 注入),决定完成时往哪推送。
	source := getSourceFromContext(ctx)
	notifyTarget := getNotifyTargetFromContext(ctx)
	taskID, err := e.svc.StartTask(ctx, userID, spec, source, notifyTarget)
	if err != nil {
		return nil, err
	}
	return &TaskReceipt{TaskID: taskID, Status: domainagent.TaskStatusPending, Message: "已安排子 Agent 处理"}, nil
}

func (e *LocalAgentExecutor) Send(ctx context.Context, taskID int64, msg AgentMessage) error {
	// 当前实现:把消息文本作为 note 补充(update_task)。
	// 需 userID,从 ctx 取(executeTask 注入)。主 Agent 调用时 ctx 已含 userID。
	userID := getUserIDFromContext(ctx)
	if userID == 0 {
		return fmt.Errorf("local executor: no user id in context")
	}
	note := ""
	for _, p := range msg.Parts {
		if p.Type == "text" && p.Text != "" {
			if note != "" {
				note += "\n"
			}
			note += p.Text
		}
	}
	if note == "" {
		return fmt.Errorf("empty message")
	}
	return e.svc.UpdateTask(userID, taskID, "", note)
}

func (e *LocalAgentExecutor) Cancel(ctx context.Context, taskID int64) error {
	userID := getUserIDFromContext(ctx)
	if userID == 0 {
		return fmt.Errorf("local executor: no user id in context")
	}
	return e.svc.CancelTask(userID, taskID)
}

func (e *LocalAgentExecutor) Status(ctx context.Context, taskID int64) (*AgentTaskStatus, error) {
	task, err := e.svc.GetTask(taskID)
	if err != nil {
		return nil, err
	}
	return &AgentTaskStatus{TaskID: task.ID, Status: task.Status, Goal: task.Goal}, nil
}

// compile-time: 保证 LocalAgentExecutor 实现 AgentExecutor
var _ AgentExecutor = (*LocalAgentExecutor)(nil)
