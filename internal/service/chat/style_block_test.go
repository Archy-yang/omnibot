package chat

// 风格注入块(M2,24号 §6 修订版):BuildContextMessages 在记忆常驻块之后追加
// 风格段 system 消息(同一 per-user 注入通道)。空串 = 不注入,零回归。

import (
	"context"
	"fmt"
	"testing"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
	"omnibot/internal/repository/chat"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockStyleSegmentProvider struct {
	segment string
	userID  int64
}

func (m *mockStyleSegmentProvider) StylePromptSegment(_ context.Context, userID int64) string {
	m.userID = userID
	return m.segment
}

func TestMessageService_BuildContextMessages_StyleBlock(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	style := &mockStyleSegmentProvider{segment: "== 用户的沟通风格(参考) ==\n- [tone] 说话随性"}
	service := NewMessageService(msgRepo, MessageServiceDeps{Style: style})

	msgRepo.Create(conversation.NewUserMessage(123, "你好", "wx_1"))
	msgRepo.Create(conversation.NewAssistantMessage(123, "你好!"))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前消息")
	require.NoError(t, err)

	// 风格块恰好一条 system 消息,且在记忆块之后、当前消息之前
	var idx []int
	for i, m := range ctxMsgs {
		if m.Role == conversation.RoleSystem && m.Content == style.segment {
			idx = append(idx, i)
		}
	}
	require.Len(t, idx, 1, "风格段应恰好注入一次")
	assert.Equal(t, int64(123), style.userID, "provider 应收到真实 userID")
	assert.Less(t, idx[0], len(ctxMsgs)-1, "风格段不应出现在当前用户消息之后")
}

func TestMessageService_BuildContextMessages_StyleEmpty(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	style := &mockStyleSegmentProvider{segment: ""}
	service := NewMessageService(msgRepo, MessageServiceDeps{Style: style})

	for i := 1; i <= 3; i++ {
		msgRepo.Create(conversation.NewUserMessage(123, fmt.Sprintf("用户消息 %d", i), fmt.Sprintf("wx_%d", i)))
		msgRepo.Create(conversation.NewAssistantMessage(123, fmt.Sprintf("回复 %d", i)))
	}

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前消息")
	require.NoError(t, err)

	for _, m := range ctxMsgs {
		assert.NotEqual(t, conversation.RoleSystem, m.Role, "空风格段 = 不注入 system 消息(开关关/无观察)")
	}
}

func TestMessageService_BuildContextMessages_StyleNil(t *testing.T) {
	testDB := db.NewTestDB(t)
	msgRepo := chat.NewMessageRepository(testDB)
	service := NewMessageService(msgRepo, MessageServiceDeps{})

	msgRepo.Create(conversation.NewUserMessage(123, "你好", "wx_1"))

	ctxMsgs, err := service.BuildContextMessages(context.Background(), 123, "当前消息")
	require.NoError(t, err)
	assert.NotEmpty(t, ctxMsgs, "未装配风格 provider = M2 未启用,零回归")
}
