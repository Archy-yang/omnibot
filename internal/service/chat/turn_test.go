package chat

import (
	"context"
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSaveUserMessage_CreatesTurnAndConversation 复现 16-架构迭代路线图 §5.2/§5.6:
// 每条 User Message 开启一个新 Turn(自动 ensureConversation);消息落库带 turn_id/conversation_id。
func TestSaveUserMessage_CreatesTurnAndConversation(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	service := NewMessageService(msgRepo, convRepo)
	ctx := context.Background()

	turnID1, err := service.SaveUserMessage(ctx, 42, "帮我调研 Claude Agent SDK", "")
	require.NoError(t, err)
	require.NotZero(t, turnID1, "第一次 SaveUserMessage 应返回新建 Turn 的 ID")

	// 用户消息落库且带逻辑归属
	var userMsg conversation.Message
	require.NoError(t, testDB.Where("user_id=? AND role=?", 42, conversation.RoleUser).Order("id DESC").First(&userMsg).Error)
	require.NotNil(t, userMsg.TurnID)
	assert.Equal(t, turnID1, *userMsg.TurnID)
	require.NotNil(t, userMsg.ConversationID)

	// conversation 存在且 active,agent=main
	var conv conversation.Conversation
	require.NoError(t, testDB.First(&conv, *userMsg.ConversationID).Error)
	assert.Equal(t, conversation.ConversationStatusActive, conv.Status)

	var ag agent.Agent
	require.NoError(t, testDB.First(&ag, conv.AgentID).Error)
	assert.Equal(t, agent.AgentCodeMain, ag.Code)

	// turn 存在且归属该 conversation
	var turn conversation.ConversationTurn
	require.NoError(t, testDB.First(&turn, turnID1).Error)
	assert.Equal(t, conv.ID, turn.ConversationID)

	// 第二条用户消息:同一 conversation,新 Turn
	turnID2, err := service.SaveUserMessage(ctx, 42, "第二个问题", "")
	require.NoError(t, err)
	require.NotZero(t, turnID2)
	assert.NotEqual(t, turnID1, turnID2, "每条用户消息开启新 Turn")

	var convCount, turnCount int64
	testDB.Model(&conversation.Conversation{}).Where("user_id=?", 42).Count(&convCount)
	testDB.Model(&conversation.ConversationTurn{}).Where("user_id=?", 42).Count(&turnCount)
	assert.Equal(t, int64(1), convCount, "同一用户复用同一 active conversation")
	assert.Equal(t, int64(2), turnCount)

	// 不同用户:各自独立 conversation
	_, err = service.SaveUserMessage(ctx, 43, "你好", "")
	require.NoError(t, err)
	testDB.Model(&conversation.Conversation{}).Where("user_id=?", 43).Count(&convCount)
	assert.Equal(t, int64(1), convCount)
}

// TestSaveAssistantMessage_AttachesContextTurn §5.6:Main Agent 普通回复关联当前 Turn。
// agent runtime 持有的 ctx 已被 handler WithTurnID 注入,消息服务从 ctx 读 TurnID 落库。
func TestSaveAssistantMessage_AttachesContextTurn(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	service := NewMessageService(msgRepo, convRepo)

	turnID, err := service.SaveUserMessage(context.Background(), 42, "问题", "")
	require.NoError(t, err)

	// 模拟 runtime:handler 注入 TurnID 后的 ctx
	ctx := agent.WithTurnID(context.Background(), turnID)
	require.NoError(t, service.SaveAssistantMessageWithSegments(ctx, 42, "回复", nil, nil))

	var reply conversation.Message
	require.NoError(t, testDB.Where("user_id=? AND role=?", 42, conversation.RoleAssistant).First(&reply).Error)
	require.NotNil(t, reply.TurnID)
	assert.Equal(t, turnID, *reply.TurnID)

	// 无 Turn 上下文(如系统路径):turn_id 留空,不报错
	require.NoError(t, service.SaveAssistantMessage(context.Background(), 42, "系统消息"))
	var sysMsg conversation.Message
	require.NoError(t, testDB.Where("user_id=? AND content=?", 42, "系统消息").First(&sysMsg).Error)
	assert.Nil(t, sysMsg.TurnID, "无 Turn 上下文时 turn_id 应为 NULL")
}

// TestSaveUserMessage_TurnFailureDegraded Turn 创建失败不阻塞对话(消息照常落库,turn_id 留空)。
func TestSaveUserMessage_TurnFailureDegraded(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo, failingConversationRepo{})
	ctx := context.Background()

	turnID, err := service.SaveUserMessage(ctx, 42, "消息", "")
	require.NoError(t, err, "Turn 创建失败不应阻塞消息落库")
	assert.Zero(t, turnID)

	var count int64
	testDB.Model(&conversation.Message{}).Where("user_id=?", 42).Count(&count)
	assert.Equal(t, int64(1), count)
}

// failingConversationRepo 故障注入:所有操作报错。
type failingConversationRepo struct{ chat.ConversationRepository }

func (failingConversationRepo) EnsureActiveConversation(userID, agentID int64) (*conversation.Conversation, error) {
	return nil, assert.AnError
}
func (failingConversationRepo) CreateTurn(conv *conversation.Conversation) (*conversation.ConversationTurn, error) {
	return nil, assert.AnError
}
func (failingConversationRepo) GetAgentByCode(code string) (*agent.Agent, error) {
	return nil, assert.AnError
}

// TestSaveReportMessage_AttachesOriginTurn §5.6:Task Report 保存原始 TurnID。
// 汇报在时间上可晚于后续 Turn,但 turn_id 必须指向最初那条请求的 Turn(§5.5)。
func TestSaveReportMessage_AttachesOriginTurn(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	service := NewMessageService(msgRepo, convRepo)

	// Turn #100 发起任务;期间用户又聊了两个 Turn(#101/#102)
	turn100, err := service.SaveUserMessage(context.Background(), 42, "帮我调研 X", "")
	require.NoError(t, err)
	_, err = service.SaveUserMessage(context.Background(), 42, "期间的问题1", "")
	require.NoError(t, err)
	_, err = service.SaveUserMessage(context.Background(), 42, "期间的问题2", "")
	require.NoError(t, err)

	// 10 分钟后汇报落地:turn_id 仍是 100
	require.NoError(t, service.SaveReportMessage(context.Background(), 42, 888, turn100, "调研完成……", nil, nil))
	var report conversation.Message
	require.NoError(t, testDB.Where("user_id=? AND kind=?", 42, conversation.KindReport).First(&report).Error)
	require.NotNil(t, report.TurnID)
	assert.Equal(t, turn100, *report.TurnID, "report 的逻辑归属应是原始 Turn")

	// 时间线顺序不受影响:report 仍在时间线最后(§5.5)
	var lastID int64
	testDB.Model(&conversation.Message{}).Where("user_id=?", 42).Order("id DESC").Limit(1).Pluck("id", &lastID)
	assert.Equal(t, report.ID, lastID)
}
