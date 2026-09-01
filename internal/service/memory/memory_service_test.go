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
	getByIDID    int64
	getByIDUser  int64
	getByIDMem   *memorydomain.Memory
	getByIDErr   error
	deletedID    int64
	deletedIDUser int64
	deleteByIDResult bool
	deleteByIDErr error
	updatedID int64
	updatedUserID int64
	updatedContent string
	updateResult *memorydomain.Memory
	updateErr error
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

func (m *mockMemoryRepository) GetByID(id int64, userID int64) (*memorydomain.Memory, error) {
	m.getByIDID = id
	m.getByIDUser = userID
	return m.getByIDMem, m.getByIDErr
}

func (m *mockMemoryRepository) DeleteByID(id int64, userID int64) (bool, error) {
	m.deletedID = id
	m.deletedIDUser = userID
	return m.deleteByIDResult, m.deleteByIDErr
}

func (m *mockMemoryRepository) UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error) {
	m.updatedID = id
	m.updatedUserID = userID
	m.updatedContent = content
	return m.updateResult, m.updateErr
}

func TestMemoryService_RememberTrimsAndSavesContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	memory, err := service.Remember(context.Background(), 123, "   我偏好简洁回答   ")

	require.NoError(t, err)
	require.NotNil(t, memory)
	assert.Equal(t, int64(123), repo.created.UserID)
	assert.Equal(t, "我偏好简洁回答", repo.created.Content)
	assert.Equal(t, int64(99), memory.ID)
}

func TestMemoryService_RememberRejectsEmptyContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	memory, err := service.Remember(context.Background(), 123, "   ")

	assert.ErrorIs(t, err, ErrEmptyContent)
	assert.Nil(t, memory)
	assert.Nil(t, repo.created)
}

func TestMemoryService_RememberRejectsTooLongContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)
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
	service := NewMemoryService(repo, nil)

	memories, err := service.List(context.Background(), 123)

	require.NoError(t, err)
	require.Len(t, memories, 1)
	assert.Equal(t, "第一条", memories[0].Content)
}

func TestMemoryService_ClearIsIdempotent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	err := service.Clear(context.Background(), 123)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.deletedUser)
}

func TestMemoryService_GetRecentForContextReturnsContents(t *testing.T) {
	repo := &mockMemoryRepository{memories: []*memorydomain.Memory{
		memorydomain.NewMemory(123, "第一条"),
		memorydomain.NewMemory(123, "第二条"),
	}}
	service := NewMemoryService(repo, nil)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	require.NoError(t, err)
	assert.Equal(t, int64(123), repo.recentUserID)
	assert.Equal(t, 10, repo.recentLimit)
	assert.Equal(t, []string{"第一条", "第二条"}, contents)
}

func TestMemoryService_PropagatesRepositoryErrors(t *testing.T) {
	expectedErr := errors.New("database down")
	repo := &mockMemoryRepository{recentErr: expectedErr}
	service := NewMemoryService(repo, nil)

	contents, err := service.GetRecentForContext(context.Background(), 123, 10)

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, contents)
}

func TestMemoryService_DeleteByID_Success(t *testing.T) {
	repo := &mockMemoryRepository{deleteByIDResult: true}
	service := NewMemoryService(repo, nil)

	deleted, err := service.Delete(context.Background(), 123, 1)

	require.NoError(t, err)
	assert.True(t, deleted)
	assert.Equal(t, int64(1), repo.deletedID)
	assert.Equal(t, int64(123), repo.deletedIDUser)
}

func TestMemoryService_DeleteByID_NotFound(t *testing.T) {
	repo := &mockMemoryRepository{deleteByIDResult: false}
	service := NewMemoryService(repo, nil)

	deleted, err := service.Delete(context.Background(), 123, 999)

	require.NoError(t, err)
	assert.False(t, deleted)
}

func TestMemoryService_DeleteByID_Error(t *testing.T) {
	expectedErr := errors.New("database error")
	repo := &mockMemoryRepository{deleteByIDErr: expectedErr}
	service := NewMemoryService(repo, nil)

	deleted, err := service.Delete(context.Background(), 123, 1)

	assert.ErrorIs(t, err, expectedErr)
	assert.False(t, deleted)
}

func TestMemoryService_Update_TrimsAndUpdatesContent(t *testing.T) {
	expectedMemory := memorydomain.NewMemory(123, "新内容")
	expectedMemory.ID = 1
	repo := &mockMemoryRepository{updateResult: expectedMemory}
	service := NewMemoryService(repo, nil)

	memory, err := service.Update(context.Background(), 123, 1, "  新内容  ")

	require.NoError(t, err)
	require.NotNil(t, memory)
	assert.Equal(t, int64(1), repo.updatedID)
	assert.Equal(t, int64(123), repo.updatedUserID)
	assert.Equal(t, "新内容", repo.updatedContent)
	assert.Equal(t, "新内容", memory.Content)
}

func TestMemoryService_Update_RejectsEmptyContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	memory, err := service.Update(context.Background(), 123, 1, "   ")

	assert.ErrorIs(t, err, ErrEmptyContent)
	assert.Nil(t, memory)
	assert.Zero(t, repo.updatedID)
}

func TestMemoryService_Update_RejectsTooLongContent(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	memory, err := service.Update(context.Background(), 123, 1, strings.Repeat("你", MaxMemoryContentLength+1))

	assert.ErrorIs(t, err, ErrContentTooLong)
	assert.Nil(t, memory)
	assert.Zero(t, repo.updatedID)
}

func TestMemoryService_Update_ReturnsNilWhenNotFound(t *testing.T) {
	repo := &mockMemoryRepository{}
	service := NewMemoryService(repo, nil)

	memory, err := service.Update(context.Background(), 123, 999, "新内容")

	require.NoError(t, err)
	assert.Nil(t, memory)
}

func TestMemoryService_Update_Error(t *testing.T) {
	expectedErr := errors.New("database error")
	repo := &mockMemoryRepository{updateErr: expectedErr}
	service := NewMemoryService(repo, nil)

	memory, err := service.Update(context.Background(), 123, 1, "新内容")

	assert.ErrorIs(t, err, expectedErr)
	assert.Nil(t, memory)
}
