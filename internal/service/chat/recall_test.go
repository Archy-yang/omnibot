package chat

import (
	"context"
	"strings"
	"testing"

	"omnibot/internal/db"
	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// topicEmbedProvider 按关键词出正交向量的桩:苹果/香蕉/樱桃各自正交,未知词为零相关。
type topicEmbedProvider struct{}

func (topicEmbedProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		switch {
		case strings.Contains(t, "苹果"):
			out[i] = []float32{1, 0, 0, 0}
		case strings.Contains(t, "香蕉"):
			out[i] = []float32{0, 1, 0, 0}
		case strings.Contains(t, "樱桃"):
			out[i] = []float32{0, 0, 1, 0}
		default:
			out[i] = []float32{0, 0, 0, 0} // 零向量与一切余弦为 0:模拟"无语义相关"
		}
	}
	return out, nil
}
func (topicEmbedProvider) Dim() int     { return 4 }
func (topicEmbedProvider) Name() string { return "stub-embed" }

// newRecallTest 组装:4 个 turn 的 chunk 索引(苹果/香蕉/樱桃/其他 各一题)。
func newRecallTest(t *testing.T) (*ChunkRecallService, *messageService, *gorm.DB, []int64) {
	t.Helper()
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	embedder := NewChunkEmbedder(
		chat.NewChunkWatermarkRepository(testDB),
		chat.NewConversationChunkRepository(testDB),
		msgRepo,
		topicEmbedProvider{},
	)
	svc := NewMessageService(msgRepo, MessageServiceDeps{
		Conversation: convRepo,
	}).(*messageService)

	var turnIDs []int64
	topics := []string{"苹果什么时候吃最好", "香蕉怎么保存", "樱桃的价格", "随便聊聊天气"}
	for _, topic := range topics {
		turnID, err := svc.SaveUserMessage(context.Background(), 42, topic, "")
		require.NoError(t, err)
		require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turnID), 42, topic+"的回复"))
		turnIDs = append(turnIDs, turnID)
	}
	require.NoError(t, embedder.RunOnce(context.Background(), 42))

	recall := NewChunkRecallService(
		chat.NewConversationChunkRepository(testDB),
		topicEmbedProvider{},
	)
	return recall, svc, testDB, turnIDs
}

// TestChunkRecallService_SearchWithNeighborExpansion §9.4:命中 turn 展开前后邻居,时序输出。
func TestChunkRecallService_SearchWithNeighborExpansion(t *testing.T) {
	recall, _, _, turnIDs := newRecallTest(t)

	// 查询命中"樱桃"(第 3 个 turn)→ 邻居展开:香蕉(2) + 樱桃(3) + 天气(4)
	out, err := recall.Search(context.Background(), 42, "樱桃多少钱", 0)
	require.NoError(t, err)
	require.NotEmpty(t, out)
	assert.Contains(t, out[0], "香蕉", "邻居展开应含命中 turn 的前一 turn,且按时序在前")
	assert.Contains(t, out[1], "樱桃")
	assert.Contains(t, out[2], "天气", "邻居展开应含命中 turn 的后一 turn")
	assert.Len(t, out, 3)
	assert.NotContains(t, strings.Join(out, "\n"), "苹果", "不相关的远端 turn 不召回")
	_ = turnIDs
}

// TestChunkRecallService_ScoreThreshold §9 验收:无语义相关(全低于阈值)不硬凑召回。
func TestChunkRecallService_ScoreThreshold(t *testing.T) {
	recall, _, _, _ := newRecallTest(t)

	out, err := recall.Search(context.Background(), 42, "火星探测器最新进展", 0)
	require.NoError(t, err)
	assert.Empty(t, out, "正交向量不得分,不召回")
}

// TestChunkRecallService_ExcludeTailWindow 尾窗已覆盖的 chunk 不复述(Recall 只补 Compact 截掉的部分)。
func TestChunkRecallService_ExcludeTailWindow(t *testing.T) {
	recall, _, testDB, _ := newRecallTest(t)

	// 樱桃 turn 的 EndMessageID:查出该 turn 的最后一条消息 id
	var lastMsg struct {
		ID      int64
		Turn    int64
		Content string
	}
	require.NoError(t, testDB.Raw(`SELECT id, turn_id AS turn, content FROM messages WHERE content LIKE '%樱桃%' ORDER BY id DESC LIMIT 1`).
		Scan(&lastMsg).Error)

	out, err := recall.Search(context.Background(), 42, "樱桃多少钱", lastMsg.ID)
	require.NoError(t, err)
	for _, c := range out {
		assert.NotContains(t, c, "樱桃", "命中 chunk 已在尾窗内,应排除")
	}
	assert.NotEmpty(t, out, "邻居 turn(香蕉/天气)不在尾窗,仍应召回")
}

// TestBuildContextMessages_RecallInjected §8.3:召回注入位置 = Recent Raw 之后、当前问题之前。
func TestBuildContextMessages_RecallInjected(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	svc := NewMessageService(msgRepo, MessageServiceDeps{
		Conversation: convRepo,
		Recall:       stubRecallSearcher{},
	}).(*messageService)

	turn1, _ := svc.SaveUserMessage(context.Background(), 42, "历史问题", "")
	require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turn1), 42, "历史回复"))

	ctxMsgs, err := svc.BuildContextMessages(context.Background(), 42, "当前问题")
	require.NoError(t, err)

	// 结构:历史 2 条 + 召回(system) + 当前问题
	require.Len(t, ctxMsgs, 4)
	assert.Equal(t, "历史问题", ctxMsgs[0].Content)
	assert.Equal(t, "历史回复", ctxMsgs[1].Content)
	assert.Equal(t, "system", ctxMsgs[2].Role, "召回块位于 Recent Raw 之后")
	assert.Contains(t, ctxMsgs[2].Content, "自动召回")
	assert.Equal(t, "当前问题", ctxMsgs[3].Content, "当前问题在最后")
}

// stubRecallSearcher 固定召回桩。
type stubRecallSearcher struct{}

func (stubRecallSearcher) Search(ctx context.Context, userID int64, query string, excludeBefore int64) ([]string, error) {
	return []string{"[user] 更早的相关讨论"}, nil
}
