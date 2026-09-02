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
	// 语义检索(12-记忆系统技术方案 §8):embedding 未配置时自动降级子串
	SearchMemories(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MemoryHit, error)
	SearchDigests(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.DigestHit, error)
	// GetMemoryInjection 常驻注入数据(注入分层,§6.5 修订):
	// 手动记忆全量(用户意志,按时间正序) + 自动记忆条数(只出存在性提示,内容走工具检索)。
	GetMemoryInjection(ctx context.Context, userID int64) (manual []string, autoCount int, err error)
}

type memoryService struct {
	repo       memoryrepo.MemoryRepository
	digestRepo memoryrepo.DigestRepository
	embedding  EmbeddingProvider // 系统默认;SetEmbeddingProvider 注入,nil=子串降级
	resolver   EmbeddingResolver // 用户级覆盖;SetEmbeddingResolver 注入,可选
}

func NewMemoryService(repo memoryrepo.MemoryRepository, digestRepo memoryrepo.DigestRepository) MemoryService {
	return &memoryService{repo: repo, digestRepo: digestRepo}
}

// EmbeddingAware 支持注入向量化 provider 的实现增强接口(可选能力,不影响记忆存取)。
// 装配点用类型断言注入,避免把 setter 塞进查询接口。
type EmbeddingAware interface {
	SetEmbeddingProvider(p EmbeddingProvider)
}

// EmbeddingResolver 按用户解析 embedding provider(用户级覆盖系统默认,12-记忆系统技术方案 §5.3)。
// 返回 nil 表示该用户无用户级配置,回落系统默认。
type EmbeddingResolver interface {
	ResolveEmbeddingProvider(userID int64) EmbeddingProvider
}

// ResolverAware 支持注入用户级解析器的实现增强接口。
type ResolverAware interface {
	SetEmbeddingResolver(r EmbeddingResolver)
}

// SetEmbeddingProvider 注入系统默认向量化 provider(可选能力,不影响记忆存取)。
func (s *memoryService) SetEmbeddingProvider(p EmbeddingProvider) {
	s.embedding = p
}

// SetEmbeddingResolver 注入用户级 provider 解析器(命中时优先于系统默认)。
func (s *memoryService) SetEmbeddingResolver(r EmbeddingResolver) {
	s.resolver = r
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

// GetMemoryInjection 常驻注入数据:手动记忆全量 + 自动记忆条数。
// 注入分层(§6.5 修订):手动=用户意志,常驻;自动=助手笔记,量无界且有噪声风险,只提示存在,内容走 search_memories。
func (s *memoryService) GetMemoryInjection(ctx context.Context, userID int64) (manual []string, autoCount int, err error) {
	manuals, err := s.repo.ListManualByUserID(userID)
	if err != nil {
		return nil, 0, err
	}
	manual = make([]string, 0, len(manuals))
	for _, m := range manuals {
		manual = append(manual, m.Content)
	}
	auto, err := s.repo.CountByUserIDAndSource(userID, memorydomain.MemorySourceAuto)
	if err != nil {
		// 计数失败不影响手动注入,只不出提示行
		logger.WarnWithFields("memory: 自动记忆计数失败,注入缺存在性提示",
			zap.Int64("user_id", userID), zap.Error(err))
		return manual, 0, nil
	}
	return manual, int(auto), nil
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
