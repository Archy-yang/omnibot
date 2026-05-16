package chat

import (
	"context"
	"errors"

	"omnibot/internal/client/llm"
	"omnibot/internal/domain/conversation"
	chatrepo "omnibot/internal/repository/chat"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 上下文轮数配置
const (
	ContextRounds          = 10 // 保留最近 10 轮对话
	ContextMessagesPerRound = 2  // 每轮 2 条消息（user + assistant）
	MaxContextMessages      = ContextRounds * ContextMessagesPerRound
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
}

type messageService struct {
	msgRepo chatrepo.MessageRepository
}

// NewMessageService 创建消息服务
func NewMessageService(msgRepo chatrepo.MessageRepository) MessageService {
	return &messageService{msgRepo: msgRepo}
}

// BuildContextMessages 构建上下文消息列表
func (s *messageService) BuildContextMessages(ctx context.Context, userID int64, currentContent string) ([]llm.ChatMessage, error) {
	// 查询最近 N 条历史消息
	messages, err := s.msgRepo.GetRecentByUserID(userID, MaxContextMessages)
	if err != nil {
		// 降级策略：查询失败时记录日志，返回空上下文，不影响使用
		logger.ErrorWithFields("Failed to get recent messages, degraded to no context",
			zap.Int64("user_id", userID),
			zap.Error(err),
		)
		messages = nil
	}

	// 转换为 LLM 消息格式
	result := make([]llm.ChatMessage, 0, len(messages)+1)

	// 添加历史消息
	for _, msg := range messages {
		result = append(result, llm.ChatMessage{
			Role:    msg.Role,
			Content: msg.Content,
		})
	}

	// 添加当前消息
	result = append(result, llm.ChatMessage{
		Role:    conversation.RoleUser,
		Content: currentContent,
	})

	return result, nil
}

// SaveUserMessage 保存用户消息
func (s *messageService) SaveUserMessage(ctx context.Context, userID int64, content string, msgID string) error {
	// 去重检查
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

	msg := conversation.NewUserMessage(userID, content, msgID)
	return s.msgRepo.Create(msg)
}

// SaveAssistantMessage 保存助手消息
func (s *messageService) SaveAssistantMessage(ctx context.Context, userID int64, content string) error {
	msg := conversation.NewAssistantMessage(userID, content)
	return s.msgRepo.Create(msg)
}
