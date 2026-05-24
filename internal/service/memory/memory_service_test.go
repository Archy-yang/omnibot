package memory

import (
	"context"
	"errors"
	"strings"
	"testing"

	memorydomain "omnibot/internal/domain/memory"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockMemoryRepository struct {
	created      *memorydomain.Memory
	memories     []*memorydomain.Memory
	createErr    error
	listErr      error
	deleteErr    error
	recentErr    error
	deletedUser  int64
	recentLimit  int
	recentUserID int64
}

func (m *mockMemoryRepository) Create(memory *memorydomain.Memory) error {
	m.created = memory
	if m.createErr != nil {
		return m.createErr
	}
	memory.ID = 99
	return nil
}

func (m *mockMemoryRepository) ListByUserID(userID int64) ([]*memorydomain.Memory, error) {
	return m.memories, m.listErr
}

func (m *mockMemoryRepository) DeleteByUserID(userID int64) error {
	m.deletedUser = userID
	return m.deleteErr
}

func (m *mockMemoryRepository) GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error) {
	m.recentUserID = userID
	m.recentLimit = limit
	return m.memories, m.recentErr
}

func TestMemoryService_RememberTrimsAndSavesContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	memory, err := service.Remember(context.Background(), 123, "   我偏好简洁回答   ")

	require.NoError(t, err)
	require.NotNil(t, memory)
	assert.Equal(t, int64(123), repo.created.UserID)
	assert.Equal(t, "我偏好简洁回答", repo.created.Content)
	assert.Equal(t, int64(99), memory.ID)
}

func TestMemoryService_RememberRejectsEmptyContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	memory, err := service.Remember(context.Background(), 123, "   ")

	assert.ErrorIs(t, err, ErrEmptyContent)
	assert.Nil(t, memory)
	assert.Nil(t, repo.created)
}

func TestMemoryService_RememberRejectsTooLongContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)
	content := strings.Repeat("你", MaxMemoryContentLength+1)

	memory, err := service.Remember(context.Background(), 123, content)

	assert.ErrorIs(t, err, ErrContentTooLong)
	assert.Nil(t, memory)
	assert.Nil(t, repo.created)
}

func TestMemoryService_ListReturnsRepositoryMemories(t *testing.T) {
	repo := &mockMemoryRepository{memories: []*memorydomain.Memory{
		memorydomain.NewMemory(123, "第一条"),
	}}
	service := NewMemoryService(repo)

	memories, err := service.List(context.Background(), 123)

	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, "第一条", memories[0].Content)
}

func TestMemoryService_ClearIsIdempotent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo)

	err := service.Clear(context.Background(), 123)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.deletedUser)
}

func TestMemoryService_GetRecentForContextReturnsContents(t *testing.T) {
	repo := &mockMemoryRepository{memories: []*memorydomain.Memory{
		memorydomain.NewMemory(123, "第一条"),
		memorydomain.NewMemory(123, "第二条"),
	}}
	service := NewMemoryService(repo)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.recentUserID)
	assert.Equal(t, 10, repo.recentLimit)
	assert.Equal(t, []string{"第一条", "第二条"}, contents)
}

func TestMemoryService_PropagatesRepositoryErrors(t *testing.T) {
	expectedErr := errors.New("database down")
	repo := &mockMemoryRepository{recentErr: expectedErr}
	service := NewMemoryService(repo)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, contents)
}
