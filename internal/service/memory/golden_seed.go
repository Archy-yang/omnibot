package memory

// golden_seed.go — 金样本预置(24 号 M3):E1 测试与 E2 真实回放(eval-memory 工具)
// 共用同一套预置逻辑,保证两边跑的是同构输入。

import (
	"errors"
	"fmt"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"gorm.io/gorm"
)

// GoldenDBSource 金样本 scratch DB 的消息源(E1 测试与 E2 工具共用):
// 语义与生产 ConversationSource 一致,只是读的是隔离 DB。
type GoldenDBSource struct {
	DB *gorm.DB
}

// GetLatestMessageID 最新消息 ID(空库返回 0)。
func (s *GoldenDBSource) GetLatestMessageID(userID int64) (int64, error) {
	var m conversation.Message
	err := s.DB.Where("user_id = ?", userID).Order("id DESC").First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return m.ID, nil
}

// GetRangeByUserID 区间 (afterID, toID] 的消息。
func (s *GoldenDBSource) GetRangeByUserID(userID, afterID, toID int64) ([]*conversation.Message, error) {
	var out []*conversation.Message
	err := s.DB.Where("user_id = ? AND id > ? AND id <= ?", userID, afterID, toID).Order("id").Find(&out).Error
	return out, err
}

// SeedGoldenFixture 把金样本预置进一个空 DB(消息/事项/loop)。
// 返回最后一条消息 ID(作 ConversationSource.latest)。
// 消息 ID 自增 1..N,scripted_output 的 source_message_ids 与 loop 的显式 ID(101 起)
// 都按此约定引用。
func SeedGoldenFixture(db *gorm.DB, f *GoldenFixture) (int64, error) {
	lastID := int64(0)
	for i, m := range f.Messages {
		row := &conversation.Message{
			UserID:    f.UserID,
			Role:      m.Role,
			Content:   m.Content,
			CreatedAt: m.CreatedAt,
		}
		if err := db.Create(row).Error; err != nil {
			return 0, fmt.Errorf("seed message %d: %w", i+1, err)
		}
		lastID = row.ID
	}

	matterRepo := memoryrepo.NewMatterRepository(db)
	for _, sm := range f.Snapshot.Matters {
		if err := matterRepo.UpsertByTitle(&memorydomain.Matter{
			UserID:    f.UserID,
			Title:     sm.Title,
			StateDesc: sm.StateDesc,
			Status:    sm.Status,
		}); err != nil {
			return 0, fmt.Errorf("seed matter %s: %w", sm.Title, err)
		}
	}

	for _, sl := range f.Snapshot.Loops {
		if err := db.Create(&memorydomain.Memory{
			ID:         sl.ID,
			UserID:     f.UserID,
			Content:    sl.Content,
			Kind:       memorydomain.MemoryKindLoop,
			LoopStatus: memorydomain.MemoryLoopStatusOpen,
			Source:     "auto",
		}).Error; err != nil {
			return 0, fmt.Errorf("seed loop %d: %w", sl.ID, err)
		}
	}
	return lastID, nil
}
