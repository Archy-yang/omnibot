package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	chatrepo "omnibot/internal/repository/chat"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 上下文轮数配置
const (
	ContextRounds           = 10 // 保留最近 10 轮对话
	ContextMessagesPerRound = 2  // 每轮 2 条消息（user + assistant）
	MaxContextMessages      = ContextRounds * ContextMessagesPerRound
	MaxContextMemories      = 10
)

// 错误定义
var (
	ErrDuplicateMessage = errors.New("duplicate message")
)

// MessageService 消息服务接口
type MessageService interface {
	// BuildContextMessages 构建上下文消息列表（历史消息 + 当前消息）
	BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error)

	// SaveUserMessage 保存用户消息
	SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error

	// SaveAssistantMessage 保存助手消息
	SaveAssistantMessage(ctx context.Context, userID int64, content string) error

	// SaveAssistantMessageWithSegments 保存带思考过程片段的助手消息（v1.5.4），
	// 并落 Agent 运行步骤链（v1.5.5）。content 为纯文本投影，segments 为展示片段，
	// steps 为该轮的有序执行步骤（LLM 调用 + 工具调用），保存消息后 stamp MessageID 批量落库；
	// 为空时不写。步骤落库失败不影响消息持久化（仅记日志）。
	SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error
	// SaveAssistantMessageWithToolCalls 同上,并支持规范改造:toolCalls 为工具调用配对 JSON
	// (nil 表示无工具调用),落 Message.ToolCalls 供跨轮重建上下文。
	SaveAssistantMessageWithToolCalls(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, toolCalls *string, steps []*conversation.AgentStep) error

	// ListByUser 获取用户的历史消息（按时间正序，旧的在前）。
	// before 为 0 时返回最近 limit 条；before > 0 时返回 ID 小于 before 的最近 limit 条，用于翻页。
	ListByUser(ctx context.Context, userID int64, limit int, before int64) ([]*conversation.Message, error)
}

// LongTermMemoryProvider 长期记忆上下文提供者
type LongTermMemoryProvider interface {
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
}

type messageService struct {
	msgRepo      chatrepo.MessageRepository
	memorySvc    LongTermMemoryProvider
	stepRepo     chatrepo.AgentStepRepository
}

// NewMessageService 创建消息服务
func NewMessageService(msgRepo chatrepo.MessageRepository, optionalServices ...interface{}) MessageService {
	service := &messageService{msgRepo: msgRepo}
	for _, svc := range optionalServices {
		switch s := svc.(type) {
		case LongTermMemoryProvider:
			service.memorySvc = s
		case chatrepo.AgentStepRepository:
			service.stepRepo = s
		}
	}
	return service
}

// BuildContextMessages 构建上下文消息列表
func (s *messageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	memoryMessages := s.buildLongTermMemoryMessages(ctx, userID)

	messages, err := s.msgRepo.GetRecentByUserID(userID, MaxContextMessages)
	if err != nil {
		logger.ErrorWithFields("Failed to get recent messages, degraded to no context",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		messages = nil
	}

	result := make([]llm.ChatMessage, 0, len(memoryMessages)+len(messages)+1)
	result = append(result, memoryMessages...)

	for _, msg := range messages {
		// 规范改造:assistant 消息若带 ToolCalls(调过工具),展开成 OpenAI 配对序列:
		// assistant(tool_calls) -> tool(result, tool_call_id) x N -> assistant(最终回复)
		// 这样下一轮 LLM 能看到"之前调过工具",避免历史只剩纯文本导致不调工具。
		if msg.Role == conversation.RoleAssistant && msg.ToolCalls != nil {
			pairs, err := parseToolCalls(*msg.ToolCalls)
			if err == nil && len(pairs) > 0 {
				// assistant(tool_calls) 消息(content 空,tool_calls 带调用)
				toolCalls := make([]map[string]interface{}, 0, len(pairs))
				for _, p := range pairs {
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   p.ID,
						"type": "function",
						"function": map[string]interface{}{
							"name":      p.Name,
							"arguments": p.Arguments,
						},
					})
				}
				result = append(result, llm.ChatMessage{
					Role:      conversation.RoleAssistant,
					ToolCalls: toolCalls,
				})
				// 每个 tool result 一条 tool 消息
				for _, p := range pairs {
					result = append(result, llm.ChatMessage{
						Role:       conversation.RoleTool,
						Content:    p.Result,
						ToolCallID: p.ID,
					})
				}
			}
		}
		result = append(result, llm.ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	result = append(result, llm.ChatMessage{
		Role:    conversation.RoleUser,
		Content: currentContent,
	})

	return result, nil
}

func (s *messageService) buildLongTermMemoryMessages(ctx context.Context, userID int64) []llm.ChatMessage {
	if s.memorySvc == nil || userID <= 0 {
		return nil
	}

	memories, err := s.memorySvc.GetRecentForContext(ctx, userID, MaxContextMemories)
	if err != nil {
		logger.ErrorWithFields("Failed to get long-term memories, degraded to short-term context only",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		return nil
	}
	if len(memories) == 0 {
		return nil
	}

	var builder strings.Builder
	builder.WriteString("以下是用户长期记忆，请在回答时自然参考，不要主动提及“我参考了记忆”：\n\n")
	for i, memory := range memories {
		builder.WriteString(fmt.Sprintf("%d. %s", i+1, memory))
		if i < len(memories)-1 {
			builder.WriteString("\n")
		}
	}

	return []llm.ChatMessage{{Role: conversation.RoleSystem, Content: builder.String()}}
}

// SaveUserMessage 保存用户消息
func (s *messageService) SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error {
	// 仅当传入了非空 msgID（如微信渠道）时才做去重检查；
	// Web 渠道无 msgID，不应触发去重，否则第二条消息开始会被误判为重复。
	if msgID != "" {
		exists, err := s.msgRepo.ExistsByMsgID(msgID)
		if err != nil {
			logger.ErrorWithFields("Failed to check duplicate message",
				zap.Int64("user_id", userID),
				zap.String("msg_id", msgID),
				zap.Error(err),
			)
			// 去重检查失败时，继续执行保存（宁可重复也不要丢消息）
		}
		if exists {
			return ErrDuplicateMessage
		}
	}

	msg := conversation.NewUserMessage(userID, content, msgID)
	return s.msgRepo.Create(msg)
}

// SaveAssistantMessage 保存助手消息
func (s *messageService) SaveAssistantMessage(ctx context.Context, userID int64, content string) error {
	msg := conversation.NewAssistantMessage(userID, content)
	return s.msgRepo.Create(msg)
}

// SaveAssistantMessageWithSegments 保存带思考过程片段的助手消息（v1.5.4），并落 Agent 运行步骤链（v1.5.5）。
func (s *messageService) SaveAssistantMessageWithSegments(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, steps []*conversation.AgentStep) error {
	msg := conversation.NewAssistantMessageWithSegments(userID, content, segments)
	if err := s.msgRepo.Create(msg); err != nil {
		return err
	}

	// 运行步骤链是辅助记录：消息已落库成功，步骤落库失败不应让整次保存失败，仅记日志。
	if s.stepRepo != nil && len(steps) > 0 {
		for _, step := range steps {
			step.MessageID = &msg.ID
			step.UserID = userID
		}
		if err := s.stepRepo.CreateBatch(steps); err != nil {
			logger.ErrorWithFields("Failed to save agent steps",
				zap.Int64("user_id", userID),
				zap.Int64("message_id", msg.ID),
				zap.Error(err),
			)
		}
	}
	return nil
}

// SaveAssistantMessageWithToolCalls 同 SaveAssistantMessageWithSegments,并设 ToolCalls(规范改造)。
func (s *messageService) SaveAssistantMessageWithToolCalls(ctx context.Context, userID int64, content string, segments []conversation.MessageSegment, toolCalls *string, steps []*conversation.AgentStep) error {
	msg := conversation.NewAssistantMessageWithSegments(userID, content, segments)
	msg.ToolCalls = toolCalls
	if err := s.msgRepo.Create(msg); err != nil {
		return err
	}
	if s.stepRepo != nil && len(steps) > 0 {
		for _, step := range steps {
			step.MessageID = &msg.ID
			step.UserID = userID
		}
		if err := s.stepRepo.CreateBatch(steps); err != nil {
			logger.ErrorWithFields("Failed to save agent steps",
				zap.Int64("user_id", userID),
				zap.Int64("message_id", msg.ID),
				zap.Error(err),
			)
		}
	}
	return nil
}

// ListByUser 获取用户的历史消息（按时间正序）。
// limit <= 0 时使用默认值 50，并强制上限 200，避免一次拉太多导致 DB 压力或前端渲染卡顿。
func (s *messageService) ListByUser(ctx context.Context, userID int64, limit int, before int64) ([]*conversation.Message, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if before > 0 {
		return s.msgRepo.GetByUserIDBefore(userID, before, limit)
	}
	return s.msgRepo.GetRecentByUserID(userID, limit)
}

// toolCallPair Message.ToolCalls JSON 的解析结构(规范改造)。
type toolCallPair struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Result    string `json:"result"`
}

// parseToolCalls 解析 Message.ToolCalls JSON 字段。
// 失败返回 error(调用方降级为不展开配对,仅用 content)。
func parseToolCalls(s string) ([]toolCallPair, error) {
	if s == "" {
		return nil, nil
	}
	var pairs []toolCallPair
	if err := json.Unmarshal([]byte(s), &pairs); err != nil {
		return nil, err
	}
	return pairs, nil
}
