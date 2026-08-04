package agent

import "time"

// TaskSpec 任务包:主 Agent 派活时传给子 Agent的任务合同(替代裸 goal)。
//
// 设计目的(见 10-多Agent通讯协议演进规划 §2.1):delegate 之前只传 goal 字符串,
// 子 Agent 无背景/交付物/完成标准,靠 LLM 自觉判断"做到什么程度算完",导致循环不收敛
// (task 15/16/17)。TaskSpec 让子 Agent 明确:目标是什么、必须交付什么、什么情况算完成、
// 有什么预算限制。
//
// 存 agent_tasks.task_spec(JSONB)。子 Agent runner 把它注入 system prompt。
type TaskSpec struct {
	Goal               string         `json:"goal"`                          // 目标是什么(必填,原 delegate 的 goal)
	Background         map[string]any `json:"background,omitempty"`          // 背景(项目/技术栈/当前架构等)
	Deliverables       []Deliverable  `json:"deliverables,omitempty"`        // 必须交付什么
	CompletionCriteria []string       `json:"completion_criteria,omitempty"` // 什么情况算完成
	Constraints        *Constraints   `json:"constraints,omitempty"`         // 预算和限制
}

// Deliverable 交付物定义。
type Deliverable struct {
	Name        string `json:"name"`        // 交付物名,如 "candidate_list"
	Description string `json:"description"` // 描述,如 "候选框架列表"
}

// Constraints 任务约束。
type Constraints struct {
	MaxSteps    int       `json:"max_steps,omitempty"`    // 最大推理步数(覆盖 card.MaxSteps,0 表示用 card 默认)
	MaxToolCalls int      `json:"max_tool_calls,omitempty"` // 最大工具调用次数(预留,当前未强制)
	Deadline    time.Time `json:"deadline,omitempty"`      // 截止时间(预留,当前由 card.Timeout 兜底)
}

// NewTaskSpec 从 goal 构造最小 TaskSpec(仅 goal,其余空)。
// 兼容老 delegate 只传 goal 的场景。
func NewTaskSpec(goal string) TaskSpec {
	return TaskSpec{Goal: goal}
}

// HasDetail 是否含除 goal 外的详细信息(用于判断要不要展开注入 prompt)。
func (t TaskSpec) HasDetail() bool {
	return len(t.Background) > 0 || len(t.Deliverables) > 0 ||
		len(t.CompletionCriteria) > 0 || t.Constraints != nil
}
