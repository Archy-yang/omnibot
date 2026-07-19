package conversation

import (
	"time"
)

// AgentStep 是一轮对话（一次 RunStream）中的一个执行步骤（v1.5.5 运行链路记录）。
//
// 一轮 ReAct 循环由有序步骤组成：LLM 调用 → 工具调用 → LLM 调用 → ...，靠
// MessageID + Seq 还原完整时序（WHERE message_id=? ORDER BY seq）。这是离线记录/分析表，
// 不进运行时上下文、不影响模型看到什么——运行时与记录是两条独立的路。
//
// 字段按 Kind 区分：
//   - llm_call：Request=发出的 messages JSON，Response={content, tool_calls} JSON，Model=模型名
//   - tool_call：Tool=工具名，Request=arguments，Response=原始未脱敏结果（展示脱敏走 segments）
//
// 与展示用 Message.Segments 的分工：segments.result 脱敏对外，agent_steps 存完整原始供分析。
type AgentStep struct {
	ID         int64     `gorm:"primaryKey;autoIncrement"`
	UserID     int64     `gorm:"index;not null"`          // 用户 ID
	MessageID  *int64    `gorm:"index"`                   // 主 Agent 步骤:锚到 assistant 消息 ID;子 Agent 步骤为 nil
	TaskID     *int64    `gorm:"index"`                   // 子 Agent 步骤:锚到 agent_tasks ID;主 Agent 步骤为 nil
	Seq        int       `gorm:"not null"`                // 链内顺序 0,1,2...
	Kind       string    `gorm:"size:20;index;not null"`  // llm_call / tool_call
	Status     string    `gorm:"size:20;index"`           // success / error / not_found
	DurationMs int64     // 本步耗时（毫秒）
	Tool       string    `gorm:"size:100;index"` // tool_call 用：工具名
	Model      string    `gorm:"size:100"`       // llm_call 用：模型名（best-effort）
	Request    string    `gorm:"type:text"`      // llm: messages JSON；tool: arguments
	Response   string    `gorm:"type:text"`      // llm: {content,tool_calls} JSON；tool: 原始结果
	// token 用量：预留列，本轮恒 0；将来加 stream_options.include_usage 后填充。
	PromptTokens     int
	CompletionTokens int
	CreatedAt        time.Time `gorm:"not null"`
}

// 步骤类型
const (
	StepKindLLMCall  = "llm_call"  // 一次 LLM 调用
	StepKindToolCall = "tool_call" // 一次工具调用
)

// 步骤状态
const (
	StepStatusSuccess  = "success"   // 成功
	StepStatusError    = "error"     // 执行返回 error
	StepStatusNotFound = "not_found" // 工具不存在
)

// TableName 指定表名
func (AgentStep) TableName() string {
	return "agent_steps"
}

// NewLLMStep 创建一个 LLM 调用步骤。MessageID/Seq 构造时未知，由上层 stamp。
func NewLLMStep(userID int64, request, response, model, status string, durationMs int64) *AgentStep {
	return &AgentStep{
		UserID:     userID,
		Kind:       StepKindLLMCall,
		Status:     status,
		DurationMs: durationMs,
		Model:      model,
		Request:    request,
		Response:   response,
		CreatedAt:  time.Now(),
	}
}

// NewToolStep 创建一个工具调用步骤。Response 存原始未脱敏结果（供记录/分析）。
func NewToolStep(userID int64, tool, request, response, status string, durationMs int64) *AgentStep {
	return &AgentStep{
		UserID:     userID,
		Kind:       StepKindToolCall,
		Status:     status,
		DurationMs: durationMs,
		Tool:       tool,
		Request:    request,
		Response:   response,
		CreatedAt:  time.Now(),
	}
}
