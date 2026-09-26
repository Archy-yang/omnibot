package memory

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"omnibot/internal/domain/conversation"
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
	// ClearSource 按来源清空(记忆抽屉双 tab;source 取 MemorySourceManual/Auto)。
	ClearSource(ctx context.Context, userID int64, source string) error
	GetRecentForContext(ctx context.Context, userID int64, limit int) ([]string, error)
	Delete(ctx context.Context, userID int64, memoryID int64) (bool, error)
	Update(ctx context.Context, userID int64, memoryID int64, content string) (*memorydomain.Memory, error)
	// 语义检索(12-记忆系统技术方案 §8):embedding 未配置时自动降级子串
	SearchMemories(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MemoryHit, error)
	// SearchMatters 事项优先检索(M6.2 两段式第一段):命中事项返回其状态+挂靠记忆全景。
	SearchMatters(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MatterHit, error)
	// SearchRecentMessages 中期记忆检索(M7 §10.6):消息级向量 + 时间加权,原文直达。
	// embedding 未配置/无向量时返回空(中期层静默缺失,不报错)。
	SearchRecentMessages(ctx context.Context, userID int64, query string, topK int) ([]memorydomain.MessageHit, error)
	// GetMemoryInjection 常驻注入数据(注入分层,§6.5 修订 + M8.3 §14.2.4):
	// Manual=手动全量(用户意志,时间正序);PinnedAuto=置顶自动记忆(pinned_at 倒序,
	// 唯一进常驻的自动记忆);AutoCount=自动记忆总数(注入端换算未列出条数)。
	GetMemoryInjection(ctx context.Context, userID int64) (*MemoryInjection, error)
	// SetPinned 置顶/取消置顶(M8.3)。返回是否命中(他人/不存在 → false,handler 映射 404)。
	SetPinned(ctx context.Context, userID int64, memoryID int64, pinned bool) (bool, error)
}

// MemoryInjection 常驻注入数据(§6.5 + M8.3 §14.2.4)。
type MemoryInjection struct {
	Manual     []string // 手动记忆全量(用户主动交代)
	PinnedAuto []string // 置顶自动记忆(常驻 core 例外,新近置顶优先)
	AutoCount  int      // 自动记忆总数
}

// RecentMessageSource 中期记忆原文回表(M7 §10.6):命中消息向量后取 content+时间。
// 由 chat 消息仓储适配(GetByIDs)。
type RecentMessageSource interface {
	GetByIDs(ids []int64) ([]*conversation.Message, error)
}

type memoryService struct {
	repo       memoryrepo.MemoryRepository
	matterRepo memoryrepo.MatterRepository // M6.2:事项优先检索;nil=无事项层(永不命中)
	// M7 中期记忆:消息向量 + 原文回表;任一为 nil 则中期层静默缺失
	msgEmbRepo memoryrepo.MessageEmbeddingRepository
	msgSource  RecentMessageSource
	embedding  EmbeddingProvider // 系统默认;SetEmbeddingProvider 注入,nil=子串降级
	resolver   EmbeddingResolver // 用户级覆盖;SetEmbeddingResolver 注入,可选
}

func NewMemoryService(repo memoryrepo.MemoryRepository, matterRepo memoryrepo.MatterRepository, msgEmbRepo memoryrepo.MessageEmbeddingRepository, msgSource RecentMessageSource) MemoryService {
	return &memoryService{repo: repo, matterRepo: matterRepo, msgEmbRepo: msgEmbRepo, msgSource: msgSource}
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

// GetMemoryInjection 常驻注入数据:手动全量 ∪ 置顶自动(M8.3),自动计数供存在性提示。
// 注入分层(§6.5 修订):手动=用户意志,常驻;自动=助手笔记,默认只提示存在、内容走
// search_memories;唯一例外是 pinned=true 的自动记忆(用户标记常驻,§14.2.4)。
func (s *memoryService) GetMemoryInjection(ctx context.Context, userID int64) (*MemoryInjection, error) {
	manuals, err := s.repo.ListManualByUserID(userID)
	if err != nil {
		return nil, err
	}
	manual := make([]string, 0, len(manuals))
	for _, m := range manuals {
		manual = append(manual, m.Content)
	}
	pinned, err := s.repo.ListPinnedAutoByUserID(userID)
	if err != nil {
		// 置顶列表读取失败降级为无置顶,不阻断手动注入
		logger.WarnWithFields("memory: 置顶自动记忆读取失败,本轮常驻缺置顶层",
			zap.Int64("user_id", userID), zap.Error(err))
		pinned = nil
	}
	pinnedAuto := make([]string, 0, len(pinned))
	for _, m := range pinned {
		pinnedAuto = append(pinnedAuto, m.Content)
	}
	auto, err := s.repo.CountByUserIDAndSource(userID, memorydomain.MemorySourceAuto)
	if err != nil {
		// 计数失败不影响常驻注入,只不出提示行
		logger.WarnWithFields("memory: 自动记忆计数失败,注入缺存在性提示",
			zap.Int64("user_id", userID), zap.Error(err))
		return &MemoryInjection{Manual: manual, PinnedAuto: pinnedAuto}, nil
	}
	return &MemoryInjection{Manual: manual, PinnedAuto: pinnedAuto, AutoCount: int(auto)}, nil
}

// SetPinned 置顶/取消置顶(M8.3 §14.2.4):用户在记忆抽屉把某条自动记忆标记常驻。
func (s *memoryService) SetPinned(ctx context.Context, userID int64, memoryID int64, pinned bool) (bool, error) {
	ok, err := s.repo.SetPinned(memoryID, userID, pinned)
	if err != nil {
		logger.ErrorWithFields("Failed to set memory pinned",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.Bool("pinned", pinned),
			zap.Error(err),
		)
		return false, err
	}
	if ok {
		logger.InfoWithFields("Memory pinned state changed",
			zap.Int64("user_id", userID),
			zap.Int64("memory_id", memoryID),
			zap.Bool("pinned", pinned),
			zap.String("operation", "memory_pin"),
		)
	}
	return ok, nil
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

// ClearSource 按来源清空(source 仅接受 manual/auto,由 handler 校验)。
func (s *memoryService) ClearSource(ctx context.Context, userID int64, source string) error {
	if err := s.repo.DeleteByUserIDAndSource(userID, source); err != nil {
		logger.ErrorWithFields("Failed to clear memories by source",
			zap.Int64("user_id", userID),
			zap.String("source", source),
			zap.String("operation", "memory_clear_source"),
			zap.Error(err),
		)
		return err
	}
	logger.InfoWithFields("Memories cleared by source",
		zap.Int64("user_id", userID),
		zap.String("source", source),
		zap.String("operation", "memory_clear_source"),
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
