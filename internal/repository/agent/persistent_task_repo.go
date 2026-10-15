package agent

import (
	"time"

	"gorm.io/gorm"

	"omnibot/internal/domain/agent"
)

// PersistentTaskRepository 持久任务仓储(25 号)。
type PersistentTaskRepository interface {
	Create(t *agent.PersistentTask) error
	GetByID(id int64) (*agent.PersistentTask, error)
	CountByUser(userID int64) (int64, error)
	// ListByUser 该用户的持久任务(创建时间倒序)。
	ListByUser(userID int64) ([]*agent.PersistentTask, error)
	// ListDue 到期待触发:enabled 且 next_run_at <= now(调度器扫描锚)。
	ListDue(now time.Time, limit int) ([]*agent.PersistentTask, error)
	ListEnabled() ([]*agent.PersistentTask, error)
	// UpdateFields 局部更新(服务层控制语义)。
	UpdateFields(id int64, fields map[string]interface{}) error
	// Delete 物理删除。
	Delete(id int64) error
	// ClaimDue CAS 抢占触发权:仅当 next_run_at 仍等于 expected 时推进调度状态
	// (next_run_at/last_run_at)。影响行数 0 = 已被抢占/状态漂移,调用方跳过。
	// last_task_id 在 StartTask 拿到实例 ID 后经 SetLastTask 回填。
	ClaimDue(id int64, expectedNext time.Time, newNext time.Time, now time.Time) (bool, error)
	// SetLastTask 回填最近实例任务 ID。
	SetLastTask(id, taskID int64) error
}

// GormPersistentTaskRepository GORM 实现
type GormPersistentTaskRepository struct {
	db *gorm.DB
}

// NewPersistentTaskRepository 创建仓储
func NewPersistentTaskRepository(db *gorm.DB) PersistentTaskRepository {
	return &GormPersistentTaskRepository{db: db}
}

func (r *GormPersistentTaskRepository) Create(t *agent.PersistentTask) error {
	return r.db.Create(t).Error
}

func (r *GormPersistentTaskRepository) GetByID(id int64) (*agent.PersistentTask, error) {
	var t agent.PersistentTask
	if err := r.db.First(&t, id).Error; err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *GormPersistentTaskRepository) CountByUser(userID int64) (int64, error) {
	var n int64
	err := r.db.Model(&agent.PersistentTask{}).Where("user_id = ?", userID).Count(&n).Error
	return n, err
}

func (r *GormPersistentTaskRepository) ListByUser(userID int64) ([]*agent.PersistentTask, error) {
	var list []*agent.PersistentTask
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC").Find(&list).Error
	return list, err
}

func (r *GormPersistentTaskRepository) ListDue(now time.Time, limit int) ([]*agent.PersistentTask, error) {
	var list []*agent.PersistentTask
	err := r.db.Where("enabled = ? AND next_run_at <= ?", true, now).
		Order("next_run_at ASC").Limit(limit).Find(&list).Error
	return list, err
}

func (r *GormPersistentTaskRepository) ListEnabled() ([]*agent.PersistentTask, error) {
	var list []*agent.PersistentTask
	err := r.db.Where("enabled = ?", true).Find(&list).Error
	return list, err
}

func (r *GormPersistentTaskRepository) UpdateFields(id int64, fields map[string]interface{}) error {
	return r.db.Model(&agent.PersistentTask{}).Where("id = ?", id).Updates(fields).Error
}

func (r *GormPersistentTaskRepository) Delete(id int64) error {
	return r.db.Delete(&agent.PersistentTask{}, id).Error
}

func (r *GormPersistentTaskRepository) ClaimDue(id int64, expectedNext, newNext, now time.Time) (bool, error) {
	res := r.db.Model(&agent.PersistentTask{}).
		Where("id = ? AND next_run_at = ?", id, expectedNext).
		Updates(map[string]interface{}{
			"next_run_at": newNext,
			"last_run_at": now,
		})
	return res.RowsAffected == 1, res.Error
}

func (r *GormPersistentTaskRepository) SetLastTask(id, taskID int64) error {
	return r.db.Model(&agent.PersistentTask{}).Where("id = ?", id).
		Update("last_task_id", taskID).Error
}
