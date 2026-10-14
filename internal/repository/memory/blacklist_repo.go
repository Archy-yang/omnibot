package memory

import (
	"errors"
	"strings"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

// BlacklistRepository 沉淀黑名单仓储(22号 §5)。
type BlacklistRepository interface {
	// ListByUserID 规则清单(created_at 升序)。
	ListByUserID(userID int64) ([]*memorydomain.MemoryBlacklist, error)
	// Create 新增规则((user_id,pattern) 唯一);重复 → ErrBlacklistDuplicate。
	Create(userID int64, pattern string) (*memorydomain.MemoryBlacklist, error)
	// Delete 删除规则(解除禁止);返回是否命中。
	Delete(userID, id int64) (bool, error)
	// DeleteMatchingMemories 删除内容包含 pattern 的存量记忆(22号 §5.3:建规则即忘掉存量);
	// 大小写不敏感包含匹配,与代码侧 strings.Contains 语义对齐。返回删除条数。
	DeleteMatchingMemories(userID int64, pattern string) (int64, error)
}

// ErrBlacklistDuplicate 规则已存在。
var ErrBlacklistDuplicate = errors.New("blacklist: rule already exists")

type blacklistRepository struct {
	db *gorm.DB
}

func NewBlacklistRepository(db *gorm.DB) BlacklistRepository {
	return &blacklistRepository{db: db}
}

func (r *blacklistRepository) ListByUserID(userID int64) ([]*memorydomain.MemoryBlacklist, error) {
	var out []*memorydomain.MemoryBlacklist
	err := r.db.Where("user_id = ?", userID).Order("created_at ASC, id ASC").Find(&out).Error
	return out, err
}

func (r *blacklistRepository) Create(userID int64, pattern string) (*memorydomain.MemoryBlacklist, error) {
	row := &memorydomain.MemoryBlacklist{UserID: userID, Pattern: pattern, CreatedAt: timeNow()}
	err := r.db.Create(row).Error
	if err != nil && isUniqueViolation(err) {
		return nil, ErrBlacklistDuplicate
	}
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *blacklistRepository) Delete(userID, id int64) (bool, error) {
	res := r.db.Where("user_id = ? AND id = ?", userID, id).Delete(&memorydomain.MemoryBlacklist{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *blacklistRepository) DeleteMatchingMemories(userID int64, pattern string) (int64, error) {
	p := "%" + strings.ToLower(pattern) + "%"
	res := r.db.Where("user_id = ? AND LOWER(content) LIKE ?", userID, p).Delete(&memorydomain.Memory{})
	return res.RowsAffected, res.Error
}

// isUniqueViolation SQLite/PG 通吃的唯一约束冲突判断。
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "UNIQUE constraint failed") || strings.Contains(msg, "duplicate key value")
}

// timeNow 供测试替换的时钟桩。
var timeNow = time.Now
