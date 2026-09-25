package chat

import (
	"fmt"
	"strings"
	"time"

	"omnibot/internal/domain/conversation"
)

// Turn 切分常量(Phase 3,§9.5:token-based 切分,不按字符截断)。
const (
	// chunkTargetTokens 单 chunk 目标大小:累积达到即收口。
	chunkTargetTokens = 800
	// chunkMaxTokens 单 chunk 硬上限:单条消息超限时独立成片(消息不可再切,保原文完整)。
	chunkMaxTokens = 1500
)

// buildTurnChunks 把一个 Turn 的消息(时间正序)切分为 1..N 个 chunk(§9.2/§9.5)。
// 短 Turn 整体一片;长 Turn 累积到 target 收口、超 max 强制收口;
// 每片保留 turn 归属、消息区间与片内序号。embedding 由调用方填充。
func buildTurnChunks(userID, conversationID, turnID int64, msgs []*conversation.Message) []*conversation.ConversationChunk {
	var chunks []*conversation.ConversationChunk
	var cur *conversation.ConversationChunk
	curTokens := 0

	closeCur := func() {
		if cur != nil {
			chunks = append(chunks, cur)
			cur = nil
			curTokens = 0
		}
	}

	for _, m := range msgs {
		text := formatMessageForCompact(m) // 与 Compact 输入同款格式,两层一致性
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		tokens := EstimateTokens(text)

		needNew := cur == nil ||
			(curTokens >= chunkTargetTokens) ||
			(curTokens+tokens > chunkMaxTokens && curTokens > 0)
		if needNew {
			closeCur()
			cur = &conversation.ConversationChunk{
				UserID:         userID,
				ConversationID: conversationID,
				TurnID:         turnID,
				Seq:            len(chunks),
				StartMessageID: m.ID,
			}
			curTokens = 0
		}
		if cur.Content != "" {
			cur.Content += "\n"
		}
		cur.Content += text
		cur.EndMessageID = m.ID
		cur.TokenCount += tokens
		curTokens += tokens
	}
	closeCur()

	// 极端情况:全部消息 content 为空 → 无 chunk
	if len(chunks) == 0 {
		return nil
	}
	now := time.Now()
	for _, c := range chunks {
		c.CreatedAt = now
		c.UpdatedAt = now
	}
	return chunks
}

// chunkContent 预览用短标识(日志/调试)。
func chunkBrief(c *conversation.ConversationChunk) string {
	return fmt.Sprintf("turn#%d seq%d [%d..%d] %dtok", c.TurnID, c.Seq, c.StartMessageID, c.EndMessageID, c.TokenCount)
}
