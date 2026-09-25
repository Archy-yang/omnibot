package chat

import (
	"strings"
	"testing"
	"time"

	"omnibot/internal/domain/conversation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testTime = time.Now()

func mkMsg(id int64, role, content string) *conversation.Message {
	return &conversation.Message{ID: id, UserID: 42, Role: role, Content: content, CreatedAt: testTime}
}

// TestBuildTurnChunks_ShortTurnSingleChunk §9.2:短 Turn 整体一片,内容含角色标注原文。
func TestBuildTurnChunks_ShortTurnSingleChunk(t *testing.T) {
	msgs := []*conversation.Message{
		mkMsg(1, conversation.RoleUser, "帮我调研 Claude Agent SDK"),
		mkMsg(2, conversation.RoleAssistant, "好的,已安排后台任务"),
	}
	chunks := buildTurnChunks(42, 7, 100, msgs)
	require.Len(t, chunks, 1)
	c := chunks[0]
	assert.Equal(t, int64(42), c.UserID)
	assert.Equal(t, int64(7), c.ConversationID)
	assert.Equal(t, int64(100), c.TurnID)
	assert.Equal(t, 0, c.Seq)
	assert.Equal(t, int64(1), c.StartMessageID)
	assert.Equal(t, int64(2), c.EndMessageID)
	assert.Contains(t, c.Content, "[user] 帮我调研 Claude Agent SDK")
	assert.Contains(t, c.Content, "[assistant] 好的,已安排后台任务")
	assert.Greater(t, c.TokenCount, 0)
}

// TestBuildTurnChunks_LongTurnSplit §9.5:长 Turn 按 token 切分,seq 保序,区间连续不重叠。
func TestBuildTurnChunks_LongTurnSplit(t *testing.T) {
	long := strings.Repeat("中", 700) // ≈700 token
	var msgs []*conversation.Message
	id := int64(10)
	for i := 0; i < 5; i++ {
		msgs = append(msgs, mkMsg(id, conversation.RoleUser, long))
		id++
		msgs = append(msgs, mkMsg(id, conversation.RoleAssistant, long))
		id++
	}
	chunks := buildTurnChunks(42, 7, 200, msgs)
	require.Greater(t, len(chunks), 1, "7000 token 的 Turn 应切多片")
	assert.LessOrEqual(t, len(chunks), 5)
	for i, c := range chunks {
		assert.Equal(t, i, c.Seq)
		assert.LessOrEqual(t, c.TokenCount, chunkMaxTokens, "单片不超过硬上限(单条消息本身可达上限)")
		if i > 0 {
			assert.Greater(t, c.StartMessageID, chunks[i-1].EndMessageID, "片间消息区间有序不重叠")
		}
	}
	// 区间覆盖全部消息:首片起点=首条,末片终点=末条
	assert.Equal(t, int64(10), chunks[0].StartMessageID)
	assert.Equal(t, id-1, chunks[len(chunks)-1].EndMessageID)
}

// TestBuildTurnChunks_EmptyContentSkipped 空 content 消息(如纯 tool_calls 行)不进 chunk。
func TestBuildTurnChunks_EmptyContentSkipped(t *testing.T) {
	msgs := []*conversation.Message{
		mkMsg(1, conversation.RoleUser, "问题"),
		mkMsg(2, conversation.RoleAssistant, "  "),
	}
	chunks := buildTurnChunks(42, 7, 300, msgs)
	require.Len(t, chunks, 1)
	assert.Equal(t, int64(1), chunks[0].EndMessageID, "空消息被跳过")
	assert.NotContains(t, chunks[0].Content, "[assistant]")
}

// TestBuildTurnChunks_ReportLabeled 汇报消息有专属标注(与 Compact 输入同款)。
func TestBuildTurnChunks_ReportLabeled(t *testing.T) {
	msgs := []*conversation.Message{
		mkMsg(1, conversation.RoleUser, "帮我查 X"),
		{ID: 3, UserID: 42, Role: conversation.RoleAssistant, Kind: conversation.KindReport, Content: "调研完成……", CreatedAt: testTime},
	}
	chunks := buildTurnChunks(42, 7, 400, msgs)
	require.Len(t, chunks, 1)
	assert.Contains(t, chunks[0].Content, "[assistant(子任务汇报)] 调研完成……")
}
