package chat

import (
	"context"
	"errors"
	"strings"
	"testing"

	"omnibot/internal/db"
	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// stubEmbedProvider 定长向量桩:向量由文本内容决定,可做相似度断言。
type stubEmbedProvider struct{ fail bool }

func (s *stubEmbedProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if s.fail {
		return nil, errors.New("embed boom")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 4)
		for j := 0; j < len(t) && j < 16; j++ {
			v[j%4] += float32(int(t[j])%10) / 10
		}
		out[i] = v
	}
	return out, nil
}
func (s *stubEmbedProvider) Dim() int     { return 4 }
func (s *stubEmbedProvider) Name() string { return "stub-embed" }

// newChunkEmbedderTest 组装 chunk 构建测试环境。
func newChunkEmbedderTest(t *testing.T, provider *stubEmbedProvider) (*ChunkEmbedder, *messageService, *gorm.DB) {
	t.Helper()
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	convRepo := chat.NewConversationRepository(testDB)
	embedder := NewChunkEmbedder(
		chat.NewChunkWatermarkRepository(testDB),
		chat.NewConversationChunkRepository(testDB),
		msgRepo,
		provider,
	)
	svc := NewMessageService(msgRepo, convRepo).(*messageService)
	return embedder, svc, testDB
}

// TestChunkEmbedder_IncrementalBuild 增量构建:水位之后按 turn 建 chunk,向量与模型落库。
func TestChunkEmbedder_IncrementalBuild(t *testing.T) {
	embedder, svc, _ := newChunkEmbedderTest(t, &stubEmbedProvider{})

	turn1, _ := svc.SaveUserMessage(context.Background(), 42, "帮我调研 Claude Agent SDK", "")
	require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turn1), 42, "已安排后台任务"))
	turn2, _ := svc.SaveUserMessage(context.Background(), 42, "另一个问题", "")
	require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turn2), 42, "回复"))

	require.NoError(t, embedder.RunOnce(context.Background(), 42))

	chunks, err := embedder.chunkRepo.ListByUserID(42)
	require.NoError(t, err)
	require.Len(t, chunks, 2, "两个 turn 各一片")
	assert.Equal(t, turn1, chunks[0].TurnID)
	assert.Equal(t, turn2, chunks[1].TurnID)
	for _, c := range chunks {
		assert.NotEmpty(t, c.Embedding, "chunk 应带向量")
		assert.Equal(t, "stub-embed", c.EmbeddingModel)
		assert.NotZero(t, c.ConversationID)
	}

	// 再跑一轮:无新消息,无变化
	require.NoError(t, embedder.RunOnce(context.Background(), 42))
	chunks2, _ := embedder.chunkRepo.ListByUserID(42)
	assert.Len(t, chunks2, 2)
}

// TestChunkEmbedder_LateReportRebuildsTurn 迟到 report 归属旧 turn → 该 turn chunk 重建,含汇报标注。
func TestChunkEmbedder_LateReportRebuildsTurn(t *testing.T) {
	embedder, svc, _ := newChunkEmbedderTest(t, &stubEmbedProvider{})

	turn1, _ := svc.SaveUserMessage(context.Background(), 42, "帮我调研 X", "")
	require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turn1), 42, "已安排"))
	require.NoError(t, embedder.RunOnce(context.Background(), 42))

	// 又聊一轮(推进水位)
	turn2, _ := svc.SaveUserMessage(context.Background(), 42, "期间的问题", "")
	require.NoError(t, svc.SaveAssistantMessage(agentdomain.WithTurnID(context.Background(), turn2), 42, "回复"))

	// 迟到的汇报落 turn1(report 消息 id 大,但归属旧 turn)
	require.NoError(t, svc.SaveReportMessage(context.Background(), 42, 888, turn1, "调研完成……", nil, nil))
	require.NoError(t, embedder.RunOnce(context.Background(), 42))

	chunks, _ := embedder.chunkRepo.ListByUserID(42)
	turn1Chunks := 0
	foundReport := false
	for _, c := range chunks {
		if c.TurnID == turn1 {
			turn1Chunks++
			foundReport = foundReport || strings.Contains(c.Content, "[assistant(子任务汇报)] 调研完成")
		}
	}
	assert.Equal(t, 1, turn1Chunks, "旧 turn 仍是一片(重建而非追加)")
	assert.True(t, foundReport, "重建后的 chunk 应包含迟到汇报的内容")
}

// TestChunkEmbedder_EmbedFailureWatermarkStalled 嵌入失败:错误上抛,水位不推进(下轮重试)。
func TestChunkEmbedder_EmbedFailureWatermarkStalled(t *testing.T) {
	embedder, svc, _ := newChunkEmbedderTest(t, &stubEmbedProvider{fail: true})

	_, _ = svc.SaveUserMessage(context.Background(), 42, "问题", "")
	require.NoError(t, svc.SaveAssistantMessage(context.Background(), 42, "回复"))

	err := embedder.RunOnce(context.Background(), 42)
	assert.Error(t, err)

	wm, err := embedder.watermarkRepo.GetByUserID(42)
	require.NoError(t, err)
	assert.Zero(t, wm.LastProcessedMsgID, "失败不推进水位")

	chunks, _ := embedder.chunkRepo.ListByUserID(42)
	assert.Empty(t, chunks)
}
