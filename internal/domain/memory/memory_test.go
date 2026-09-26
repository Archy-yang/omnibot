package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMemory(t *testing.T) {
	before := time.Now()

	m := NewMemory(123, "我偏好简洁直接的回答")

	assert.Equal(t, int64(123), m.UserID)
	assert.Equal(t, "我偏好简洁直接的回答", m.Content)
	assert.False(t, m.CreatedAt.Before(before))
	assert.False(t, m.UpdatedAt.Before(before))
}

func TestMemory_TableName(t *testing.T) {
	assert.Equal(t, "memories", Memory{}.TableName())
}

// TestNormalizeKind_EpisodeLegacyMapping M8.4 §14.2.3:episode 断源后显式归一 fact;
// MemoryKindEpisode 常量保留为历史兼容值(存量迁移可追溯),未知 kind 仍兜底 fact。
func TestNormalizeKind_EpisodeLegacyMapping(t *testing.T) {
	require.Equal(t, MemoryKindFact, NormalizeKind(MemoryKindEpisode))
	require.Equal(t, MemoryKindFact, NormalizeKind("bogus"))
	require.Equal(t, MemoryKindFact, NormalizeKind(""))
	require.Equal(t, MemoryKindFact, NormalizeKind(MemoryKindFact))
	require.Equal(t, MemoryKindLoop, NormalizeKind(MemoryKindLoop))
}
