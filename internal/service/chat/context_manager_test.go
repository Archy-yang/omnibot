package chat

import (
	"context"
	"strings"
	"testing"

	"omnibot/internal/db"
	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// stubCompactor 压缩器桩:记录调用,返回固定新 compact。
type stubCompactor struct {
	calls      int
	lastOld    string
	lastRawLen int
	fail       bool
}

func (s *stubCompactor) Compact(ctx context.Context, userID int64, oldCompact string, rawTexts []string) (string, error) {
	s.calls++
	s.lastOld = oldCompact
	s.lastRawLen = len(rawTexts)
	if s.fail {
		return "", assert.AnError
	}
	return "新Compact工作集", nil
}

// newContextTestDB 组装 Phase 2 测试环境:DB + 消息服务(可调预算/可注桩)。
func newContextTestDB(t *testing.T, compactor ContextCompactor) (*gorm.DB, *messageService, *stubCompactor) {
	t.Helper()
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	stateRepo := chat.NewContextStateRepository(testDB)
	svc := NewMessageService(msgRepo, convRepo, stateRepo, compactor).(*messageService)
	var stub *stubCompactor
	if compactor != nil {
		stub = compactor.(*stubCompactor)
	}
	return testDB, svc, stub
}

// seedTurnedMessages 造一批带 Turn 的历史消息(模拟 Phase 1 之后的正常写入),
// 返回消息 id 升序切片。
func seedTurnedMessages(t *testing.T, svc *messageService, userID int64, turns, tokensEach int) []int64 {
	t.Helper()
	content := strings.Repeat("中", tokensEach) // CJK 字 ≈ token
	var ids []int64
	for i := 0; i < turns; i++ {
		turnID, err := svc.SaveUserMessage(context.Background(), userID, content, "")
		require.NoError(t, err)
		require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turnID), userID, content))
		ids = append(ids, turnID)
	}
	return ids
}

// listMessageIDs 按时间正序取该用户全部消息 id。
func listMessageIDs(t *testing.T, testDB *gorm.DB, userID int64) []int64 {
	t.Helper()
	var all []conversation.Message
	require.NoError(t, testDB.Where("user_id=?", userID).Order("id ASC").Find(&all).Error)
	ids := make([]int64, len(all))
	for i := range all {
		ids[i] = all[i].ID
	}
	return ids
}

// TestBuildContextMessages_TokenBudgetTail §6.1 验收:Recent 不再固定 20 条,按 token 预算取尾窗。
func TestBuildContextMessages_TokenBudgetTail(t *testing.T) {
	_, svc, _ := newContextTestDB(t, &stubCompactor{})
	svc.keepRecentTokens = 50 // 预算:约 25 条 2-token 消息

	seedTurnedMessages(t, svc, 42, 30, 2) // 30 轮 = 60 条消息,每条 ≈2 token

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前消息")
	require.NoError(t, err)

	// 精确断言:60 条 × 2 token = 120,预算 50 → 尾窗恰好 25 条(含当前消息前的全部预算内历史)
	historyCount := len(ctxMsgs) - 1 // 末尾是当前消息
	assert.Equal(t, 25, historyCount, "按 token 预算截断尾窗(2 token/条 × 25 = 50)")
	assert.Equal(t, "当前消息", ctxMsgs[len(ctxMsgs)-1].Content)
}

// TestBuildContextMessages_WithinBudgetKeepsAll §6.1 反面:预算内历史一条不丢(旧逻辑硬截 20 条)。
func TestBuildContextMessages_WithinBudgetKeepsAll(t *testing.T) {
	_, svc, _ := newContextTestDB(t, &stubCompactor{})
	svc.keepRecentTokens = 5000

	seedTurnedMessages(t, svc, 42, 15, 2) // 30 条,总 token 远小于预算

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前消息")
	require.NoError(t, err)
	assert.Equal(t, 31, len(ctxMsgs), "预算内全部历史保留 + 当前消息单份")
}

// TestBuildContextMessages_CompactPrependedAsSystem §6.3:Compact 是独立 system 消息,
// 位于 Recent Raw 之前;不拼接进 system prompt 字符串(runtime 主 prompt 不受影响)。
func TestBuildContextMessages_CompactPrependedAsSystem(t *testing.T) {
	testDB, svc, _ := newContextTestDB(t, &stubCompactor{})
	svc.keepRecentTokens = 5000

	seedTurnedMessages(t, svc, 42, 3, 2)

	// 预置 Compact 状态:水位在第 4 条消息(第 2 轮 assistant)之后
	ids := listMessageIDs(t, testDB, 42)
	compactText := "之前的对话:用户在测试 Compact 注入。"
	convID := int64(0)
	var firstMsg conversation.Message
	require.NoError(t, testDB.First(&firstMsg, ids[0]).Error)
	convID = *firstMsg.ConversationID
	require.NoError(t, svc.contextStateRepo.Upsert(&conversation.ConversationContextState{
		ConversationID:        convID,
		CompactContent:        &compactText,
		CompactUntilMessageID: ids[3],
	}))

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前消息")
	require.NoError(t, err)

	// 水位之前的 4 条不出现,Compact 消息在最前
	require.NotEmpty(t, ctxMsgs)
	compactMsg := ctxMsgs[0]
	assert.Equal(t, "system", compactMsg.Role)
	assert.Equal(t, compactText, compactMsg.Content)
	// 总条数 = Compact(1) + 水位后原始 2 条 + 当前消息 1 条
	assert.Equal(t, 1+(6-4)+1, len(ctxMsgs))
	// 末尾是当前消息
	assert.Equal(t, "当前消息", ctxMsgs[len(ctxMsgs)-1].Content)
}

// TestBuildContextMessages_CompactionTriggered §7.4:中段积压超过阈值触发压缩,
// 新 Compact 注入上下文,水位推进到中段末尾;下一次构建不重复压缩(频率 ≪ agent loop)。
func TestBuildContextMessages_CompactionTriggered(t *testing.T) {
	testDB, svc, stub := newContextTestDB(t, &stubCompactor{})
	svc.keepRecentTokens = 50       // 尾窗很小
	svc.compactTriggerTokens = 30   // 中段 ≥30 token 即触发

	seedTurnedMessages(t, svc, 42, 20, 2) // 40 条 ≈80 token:尾窗 ~25 条,中段 ~15 条 ≈30 token → 触发
	ids := listMessageIDs(t, testDB, 42)

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前消息")
	require.NoError(t, err)
	assert.Equal(t, 1, stub.calls, "应触发一次压缩")
	assert.True(t, stub.lastRawLen > 0, "压缩器应收到中段原文")
	assert.Equal(t, "", stub.lastOld, "首次压缩无旧 Compact")

	// 新 Compact 以 system 消息注入
	assert.Equal(t, "system", ctxMsgs[0].Role)
	assert.Equal(t, "新Compact工作集", ctxMsgs[0].Content)

	// 水位推进:中段末尾 = 尾窗起点前一条(非最新消息)
	var state *conversation.ConversationContextState
	var conv conversation.Conversation
	require.NoError(t, testDB.First(&conv).Error)
	state, err = svc.contextStateRepo.GetByConversationID(conv.ID)
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "新Compact工作集", *state.CompactContent)
	assert.Greater(t, state.CompactUntilMessageID, int64(0))
	assert.Less(t, state.CompactUntilMessageID, ids[len(ids)-1], "水位不含尾窗消息")
	assert.Equal(t, 1, state.CompactVersion)

	// 第二次构建(无新消息):不再触发压缩
	_, err = svc.BuildContextMessages(context.Background(), 42, "再来一条")
	require.NoError(t, err)
	assert.Equal(t, 1, stub.calls, "Compact 发生频率应低于普通对话轮")
}

// TestBuildContextMessages_CompactionFailureDegrades 压缩失败降级:上下文照常构建,
// 水位不推进(下轮重试),不阻塞对话。
func TestBuildContextMessages_CompactionFailureDegrades(t *testing.T) {
	testDB, svc, stub := newContextTestDB(t, &stubCompactor{fail: true})
	svc.keepRecentTokens = 50
	svc.compactTriggerTokens = 30

	seedTurnedMessages(t, svc, 42, 20, 2)

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前消息")
	require.NoError(t, err, "压缩失败不应让上下文构建失败")
	assert.True(t, len(ctxMsgs) > 0)
	assert.Equal(t, 1, stub.calls, "失败后水位未推进,问题将持续重试")

	var conv conversation.Conversation
	require.NoError(t, testDB.First(&conv).Error)
	state, err := svc.contextStateRepo.GetByConversationID(conv.ID)
	require.NoError(t, err)
	assert.Nil(t, state, "失败不落状态,水位不推进")
}
