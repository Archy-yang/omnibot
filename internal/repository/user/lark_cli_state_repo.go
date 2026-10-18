package user

import (
	"time"

	domain "omnibot/internal/domain/user"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// LarkCLIStateRepository lark-cli 授权状态物化仓储(27 号)。
type LarkCLIStateRepository interface {
	// GetByUserID 取当前 blob;无记录返回 gorm.ErrRecordNotFound。
	GetByUserID(userID int64) (*domain.LarkCLIState, error)
	// Save 覆盖式 Upsert(version 重置为 1;仅迁移/首次落库用)。
	Save(state *domain.LarkCLIState) error
	// SaveCAS 版本 CAS 写回:WHERE user_id AND version=expected → version+1。
	// false = 他机已写(或本机过期),调用方重取重试。
	SaveCAS(userID int64, expectedVersion int64, blob []byte) (bool, error)
	// Delete 移除(用户断开授权时)。
	Delete(userID int64) error
}

type larkCLIStateRepository struct {
	db *gorm.DB
}

func NewLarkCLIStateRepository(db *gorm.DB) LarkCLIStateRepository {
	return &larkCLIStateRepository{db: db}
}

func (r *larkCLIStateRepository) GetByUserID(userID int64) (*domain.LarkCLIState, error) {
	var out domain.LarkCLIState
	err := r.db.First(&out, "user_id = ?", userID).Error
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *larkCLIStateRepository) Save(state *domain.LarkCLIState) error {
	state.Version = 1
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"state_blob", "version", "updated_at"}),
	}).Create(state).Error
}

func (r *larkCLIStateRepository) SaveCAS(userID int64, expectedVersion int64, blob []byte) (bool, error) {
	res := r.db.Model(&domain.LarkCLIState{}).
		Where("user_id = ? AND version = ?", userID, expectedVersion).
		Updates(map[string]interface{}{
			"state_blob": blob,
			"version":    expectedVersion + 1,
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *larkCLIStateRepository) Delete(userID int64) error {
	return r.db.Delete(&domain.LarkCLIState{}, "user_id = ?", userID).Error
}
