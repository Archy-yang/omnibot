package chat

import (
	"context"
	"fmt"

	"omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
)

// ConversationRepository conversations / conversation_turns / agents 的数据访问。
// Phase 1(16-架构迭代路线图 §5):SaveUserMessage 内 ensureConversation + createTurn,
// 三个渠道入口自动获得 Turn 能力。
type ConversationRepository interface {
	// GetAgentByCode 按 code 取 Agent(当前仅 "main");不存在报错。
	GetAgentByCode(code string) (*agent.Agent, error)
	// EnsureActiveConversation 取用户在某 Agent 下的 active conversation,没有则创建。
	EnsureActiveConversation(userID, agentID int64) (*conversation.Conversation, error)
	// GetActiveByUserAndAgent 只读查询 active conversation;不存在返回 (nil, nil)。
	// Context 构建路径用只读版,避免读上下文时意外建行。
	GetActiveByUserAndAgent(ctx context.Context, userID, agentID int64) (*conversation.Conversation, error)
	// CreateTurn 在对话内开启新 Turn。
	CreateTurn(conv *conversation.Conversation) (*conversation.ConversationTurn, error)
}

type conversationRepository struct {
	db *gorm.DB
}

func NewConversationRepository(db *gorm.DB) ConversationRepository {
	return &conversationRepository{db: db}
}

func (r *conversationRepository) GetAgentByCode(code string) (*agent.Agent, error) {
	var ag agent.Agent
	if err := r.db.Where("code = ?", code).First(&ag).Error; err != nil {
		return nil, fmt.Errorf("get agent by code %s: %w", code, err)
	}
	return &ag, nil
}

func (r *conversationRepository) EnsureActiveConversation(userID, agentID int64) (*conversation.Conversation, error) {
	var conv conversation.Conversation
	err := r.db.Where("user_id = ? AND agent_id = ? AND status = ?",
		userID, agentID, conversation.ConversationStatusActive).First(&conv).Error
	if err == nil {
		return &conv, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("query active conversation: %w", err)
	}
	conv = *conversation.NewConversation(userID, agentID)
	if err := r.db.Create(&conv).Error; err != nil {
		return nil, fmt.Errorf("create conversation: %w", err)
	}
	return &conv, nil
}

func (r *conversationRepository) GetActiveByUserAndAgent(_ context.Context, userID, agentID int64) (*conversation.Conversation, error) {
	var conv conversation.Conversation
	err := r.db.Where("user_id = ? AND agent_id = ? AND status = ?",
		userID, agentID, conversation.ConversationStatusActive).First(&conv).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("query active conversation: %w", err)
	}
	return &conv, nil
}

func (r *conversationRepository) CreateTurn(conv *conversation.Conversation) (*conversation.ConversationTurn, error) {
	turn := conversation.NewConversationTurn(conv.ID, conv.UserID)
	if err := r.db.Create(turn).Error; err != nil {
		return nil, fmt.Errorf("create conversation turn: %w", err)
	}
	return turn, nil
}
