package memory

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

const MaxMemoryContentLength = 200

var (
	ErrEmptyContent   = errors.New("memory content is empty")
	ErrContentTooLong = errors.New("memory content is too long")
)

type MemoryService interface {
	Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error)
	List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error)
	Clear(ctx context.Context, userID int64) error
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
	Delete(ctx context.Context, userID int64, memoryID int64) (bool, error)
	Update(ctx context.Context, userID int64, memoryID int64, content string) (*memorydomain.Memory, error)
}

type memoryService struct {
	repo memoryrepo.MemoryRepository
}

func NewMemoryService(repo memoryrepo.MemoryRepository) MemoryService {
	return &memoryService{repo: repo}
}

func (s *memoryService) Remember(ctx context.Context, userID int64, content string) (*memorydomain.Memory, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, ErrEmptyContent
	}
	if utf8.RuneCountInString(trimmed) > MaxMemoryContentLength {
		return nil, ErrContentTooLong
	}

	memory := memorydomain.NewMemory(userID, trimmed)
	if err := s.repo.Create(memory); err != nil {
		logger.ErrorWithFields("Failed to create memory",
			zap.Int64("user_id", userID),
			zap.Int("content_length", utf8.RuneCountInString(trimmed)),
			zap.String("operation", "memory_create"),
			zap.Error(err),
		)
		return nil, err
	}

	logger.InfoWithFields("Memory created",
		zap.Int64("user_id", userID),
		zap.Int64("memory_id", memory.ID),
		zap.Int("content_length", utf8.RuneCountInString(trimmed)),
		zap.String("operation", "memory_create"),
	)
	return memory, nil
}

func (s *memoryService) List(ctx context.Context, userID int64) ([]*memorydomain.Memory, error) {
	return s.repo.ListByUserID(userID)
}

func (s *memoryService) Clear(ctx context.Context, userID int64) error {
	if err := s.repo.DeleteByUserID(userID); err != nil {
		logger.ErrorWithFields("Failed to clear memories",
			zap.Int64("user_id", userID),
			zap.String("operation", "memory_clear"),
			zap.Error(err),
		)
		return err
	}

	logger.InfoWithFields("Memories cleared",
		zap.Int64("user_id", userID),
		zap.String("operation", "memory_clear"),
	)
	return nil
}

func (s *memoryService) GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error) {
	memories, err := s.repo.GetRecentByUserID(userID, limit)
	if err != nil {
		return nil, err
	}

	contents := make([]string, 0, len(memories))
	for _, memory := range memories {
		contents = append(contents, memory.Content)
	}
	return contents, nil
}

func (s *memoryService) Delete(ctx context.Context, userID int64, memoryID int64) (bool, error) {
	deleted, err := s.repo.DeleteByID(memoryID, userID)
	if err != nil {
		logger.ErrorWithFields("Failed to delete memory",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.String("operation", "memory_delete"),
			zap.Error(err),
		)
		return false, err
	}

	if deleted {
		logger.InfoWithFields("Memory deleted",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.String("operation", "memory_delete"),
		)
	}

	return deleted, nil
}

func (s *memoryService) Update(ctx context.Context, userID int64, memoryID int64, content string) (*memorydomain.Memory, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, ErrEmptyContent
	}
	if utf8.RuneCountInString(trimmed) > MaxMemoryContentLength {
		return nil, ErrContentTooLong
	}

	memory, err := s.repo.UpdateContentByID(memoryID, userID, trimmed)
	if err != nil {
		logger.ErrorWithFields("Failed to update memory",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.Int("content_length", utf8.RuneCountInString(trimmed)),
			zap.String("operation", "memory_update"),
			zap.Error(err),
		)
		return nil, err
	}

	if memory != nil {
		logger.InfoWithFields("Memory updated",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.Int("content_length", utf8.RuneCountInString(trimmed)),
			zap.String("operation", "memory_update"),
		)
	}

	return memory, nil
}
