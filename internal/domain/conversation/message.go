package conversation

import (
	"time"
)

// Message 角色常量
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
)

// MessageSegment 一条助手消息按 LLM 真实时序拆出的展示片段（v1.5.4）。
//
// Agent 回复可能是「文本 → 调工具 → 文本」的交错序列，segments 按顺序保存这些片段，
// 使刷新页面后历史消息仍能还原完整的思考过程（而非回退成纯文本）。
//
//   - text 段：用 Content
//   - tool 段：用 Tool / Label / Result（Result 已脱敏，见 web.sanitizeToolResult）
//
// omitempty 让另一类字段不进 JSON，序列化更紧凑。这是「JSON 骨架 + 实体表」混合架构
// 的碎片层：纯展示片段内联在此，未来 artifact / 异步任务等一等实体改用独立表 + 引用 id。
type MessageSegment struct {
	Type    string `json:"type"`              // "text" | "tool"
	Content string `json:"content,omitempty"` // text 段：文本内容
	Role    string `json:"role,omitempty"`    // text 段语义(C5):"thought"|"final";tool 段不用
	Tool    string `json:"tool,omitempty"`    // tool 段：工具名
	Label   string `json:"label,omitempty"`   // tool 段：面向用户的友好文案
	Result  string `json:"result,omitempty"`  // tool 段：脱敏后的工具结果
}

// Message 对话消息实体
type Message struct {
	ID        int64            `gorm:"primaryKey;autoIncrement"`
	UserID    int64            `gorm:"index;not null"`         // 用户 ID
	Role      string           `gorm:"size:20;not null;index"` // 消息角色：user/assistant/system/tool
	Content   string           `gorm:"type:text;not null"`     // 消息内容（纯文本投影，供复制/上下文/搜索）
	MsgID     *string          `gorm:"size:100;uniqueIndex"`   // 微信消息 ID（用于去重，仅 user 消息有）
	Segments  []MessageSegment `gorm:"serializer:json"`        // v1.5.4：Agent 思考过程有序片段，NULL 表示无（普通消息）
	ToolCalls *string          `gorm:"type:text"`               // 规范改造:assistant 调工具的配对 JSON [{id,name,arguments,result}],NULL 表示无
	CreatedAt time.Time        `gorm:"not null"`
}

// TableName 指定表名
func (Message) TableName() string {
	return "messages"
}

// ToolCallPair Message.ToolCalls JSON 的元素结构(规范改造)。
// 主 Agent 调工具的配对:id/name/arguments(工具调用) + result(工具结果)。
// 跨轮重建上下文时,展开成 assistant(tool_calls) -> tool(result) -> assistant(最终回复)。
type ToolCallPair struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}

// NewUserMessage 创建用户消息。
// msgID 为空字符串时（如 Web 渠道），存储为 NULL，避免在 msg_id 唯一索引上发生
// 第二条空串冲突；非空时（如微信渠道）存储具体值用于消息去重。
func NewUserMessage(userID int64, content string, msgID string) *Message {
	now := time.Now()
	msg := &Message{
		UserID:    userID,
		Role:      RoleUser,
		Content:   content,
		CreatedAt: now,
	}
	if msgID != "" {
		msg.MsgID = &msgID
	}
	return msg
}

// NewAssistantMessage 创建助手消息
func NewAssistantMessage(userID int64, content string) *Message {
	now := time.Now()
	return &Message{
		UserID:    userID,
		Role:      RoleAssistant,
		Content:   content,
		MsgID:     nil,
		CreatedAt: now,
	}
}

// NewAssistantMessageWithSegments 创建带思考过程片段的助手消息（v1.5.4）。
// content 是纯文本投影（供复制 / 上下文 / 搜索），segments 是按时序的展示片段。
// 仅 Web Agent 流式路径使用；其余路径继续用 NewAssistantMessage（segments 为 NULL）。
func NewAssistantMessageWithSegments(userID int64, content string, segments []MessageSegment) *Message {
	msg := NewAssistantMessage(userID, content)
	msg.Segments = segments
	return msg
}
