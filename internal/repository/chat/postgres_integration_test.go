//go:build postgres_integration

package chat

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/db"
	"omnibot/internal/domain/conversation"
)

func TestPostgresIntegration_MessageRepository(t *testing.T) {
	testDB := db.NewPostgresTestDB(t)
	repo := NewMessageRepository(testDB)

	for i := 1; i <= 15; i++ {
		userMsg := conversation.NewUserMessage(123, fmt.Sprintf("用户消息 %d", i), fmt.Sprintf("pg_wx_%d", i))
		require.NoError(t, repo.Create(userMsg))

		assistantMsg := conversation.NewAssistantMessage(123, fmt.Sprintf("机器人回复 %d", i))
		require.NoError(t, repo.Create(assistantMsg))
	}

	messages, err := repo.GetRecentByUserID(123, 10)
	require.NoError(t, err)
	require.Len(t, messages, 10)
	assert.Equal(t, "用户消息 11", messages[0].Content)
	assert.Equal(t, "机器人回复 15", messages[9].Content)

	exists, err := repo.ExistsByMsgID("pg_wx_15")
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = repo.ExistsByMsgID("pg_missing")
	require.NoError(t, err)
	assert.False(t, exists)

	duplicate := conversation.NewUserMessage(123, "重复消息", "pg_wx_15")
	assert.Error(t, repo.Create(duplicate))
}
