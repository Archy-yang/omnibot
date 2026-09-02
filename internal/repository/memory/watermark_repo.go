package memory

import (
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// WatermarkRepository 摘要管线水位仓储(12-记忆系统技术方案 §4.3)。
type WatermarkRepository interface {
	// GetByUserID 读水位;无记录返回零值水位(UserID 填充,LastDigestMsgID=0)。
	GetByUserID(userID int64) (*memorydomain.DigestWatermark, error)
	// Upsert 推进水位(单用户单行,重复 upsert 覆盖)。
	Upsert(userID int64, lastMsgID int64) error
}

type watermarkRepository struct {
	db *gorm.DB
}

func NewWatermarkRepository(db *gorm.DB) WatermarkRepository {
	return &watermarkRepository{db: db}
}

func (r *watermarkRepository) GetByUserID(userID int64) (*memorydomain.DigestWatermark, error) {
	var wm memorydomain.DigestWatermark
	err := r.db.Where("user_id = ?", userID).First(&wm).Error
	if err == gorm.ErrRecordNotFound {
		return &memorydomain.DigestWatermark{UserID: userID, LastDigestMsgID: 0}, nil
	}
	if err != nil {
		return nil, err
	}
	return &wm, nil
}

func (r *watermarkRepository) Upsert(userID int64, lastMsgID int64) error {
	wm := memorydomain.DigestWatermark{UserID: userID, LastDigestMsgID: lastMsgID, UpdatedAt: time.Now()}
	return r.db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_digest_msg_id", "updated_at"}),
	}).Create(&wm).Error
}
