package memory

import (
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

// MatterRepository 事项仓储(M6,助理人视角)。
type MatterRepository interface {
	// UpsertByTitle 同 (user_id,title) 覆写 StateDesc/Status/LastMsgID;不存在则创建。
	// 语义:事项状态是"覆写式"认知更新,不是追加。
	UpsertByTitle(m *memorydomain.Matter) error
	// CreateMatter 纯创建(29号 N1 对话内建档):不判存不覆写,幂等由 service 层
	// GetByTitle 判存保证——与 UpsertByTitle 的覆写语义刻意分开,勿混用。
	CreateMatter(m *memorydomain.Matter) error
	// GetByTitle 按标题精确取(对账时 fact 挂靠用);不存在返回 nil。
	GetByTitle(userID int64, title string) (*memorydomain.Matter, error)
	// ListActiveByUserID 活跃事项(updated_at 倒序;世界观快照用)。
	ListActiveByUserID(userID int64) ([]*memorydomain.Matter, error)
	// GetByID 按主键取(21号档案详情);不存在或他人 → nil,nil(调用方按未找到处理)。
	GetByID(userID, id int64) (*memorydomain.Matter, error)
	// ListByUserID 档案列表(21号):status 空=全部,updated_at 倒序。
	ListByUserID(userID int64, status string) ([]*memorydomain.Matter, error)
	// UpdateFields 用户纠正口(21号 §5.2):只允许 StateDesc/Status;nil=不改;返回是否命中。
	UpdateFields(userID, id int64, stateDesc, status *string) (bool, error)
	// UpdateTarget 目标日写路径(28号 N2):target nil=清除目标(恢复普通事项);返回是否命中。
	UpdateTarget(userID, id int64, target *time.Time) (bool, error)
	// UpdateDocInfo 文档同步信息回写(23号 §3)。
	UpdateDocInfo(userID, id int64, token, url string, syncedAt time.Time) (bool, error)
}

type matterRepository struct {
	db *gorm.DB
}

func NewMatterRepository(db *gorm.DB) MatterRepository {
	return &matterRepository{db: db}
}

func (r *matterRepository) UpsertByTitle(m *memorydomain.Matter) error {
	var existing memorydomain.Matter
	err := r.db.Where("user_id = ? AND title = ?", m.UserID, m.Title).First(&existing).Error
	if err == nil {
		existing.StateDesc = m.StateDesc
		existing.Status = m.Status
		existing.LastMsgID = m.LastMsgID
		existing.Embedding = m.Embedding // 状态变了向量要跟着换(M6.2 检索正确性)
		existing.EmbeddingModel = m.EmbeddingModel
		existing.UpdatedAt = time.Now()
		return r.db.Save(&existing).Error
	}
	if err == gorm.ErrRecordNotFound {
		return r.db.Create(m).Error
	}
	return err
}

// CreateMatter 纯创建(29号 N1):Create 语义,同 (user_id,title) 唯一索引冲突报错。
func (r *matterRepository) CreateMatter(m *memorydomain.Matter) error {
	return r.db.Create(m).Error
}

func (r *matterRepository) GetByTitle(userID int64, title string) (*memorydomain.Matter, error) {
	var m memorydomain.Matter
	err := r.db.Where("user_id = ? AND title = ?", userID, title).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *matterRepository) ListActiveByUserID(userID int64) ([]*memorydomain.Matter, error) {
	var matters []*memorydomain.Matter
	err := r.db.Where("user_id = ? AND status = ?", userID, memorydomain.MatterStatusActive).
		Order("updated_at DESC").
		Find(&matters).Error
	return matters, err
}

func (r *matterRepository) GetByID(userID, id int64) (*memorydomain.Matter, error) {
	var m memorydomain.Matter
	err := r.db.Where("user_id = ? AND id = ?", userID, id).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *matterRepository) ListByUserID(userID int64, status string) ([]*memorydomain.Matter, error) {
	q := r.db.Where("user_id = ?", userID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var matters []*memorydomain.Matter
	err := q.Order("updated_at DESC").Find(&matters).Error
	return matters, err
}

func (r *matterRepository) UpdateDocInfo(userID, id int64, token, url string, syncedAt time.Time) (bool, error) {
	res := r.db.Model(&memorydomain.Matter{}).
		Where("user_id = ? AND id = ?", userID, id).
		Updates(map[string]interface{}{
			"feishu_doc_token": token,
			"feishu_doc_url":   url,
			"synced_at":        syncedAt,
			"updated_at":       time.Now(),
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *matterRepository) UpdateFields(userID, id int64, stateDesc, status *string) (bool, error) {
	updates := map[string]interface{}{"updated_at": time.Now()}
	if stateDesc != nil {
		updates["state_desc"] = *stateDesc
	}
	if status != nil {
		updates["status"] = *status
	}
	res := r.db.Model(&memorydomain.Matter{}).Where("user_id = ? AND id = ?", userID, id).Updates(updates)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// UpdateTarget 目标日写路径(28号 N2):target nil = 清除目标(恢复普通事项)。
// 注意:gorm 对 typed-nil(*time.Time)(nil) 不按 NULL 处理,必须归一成无类型 nil。
func (r *matterRepository) UpdateTarget(userID, id int64, target *time.Time) (bool, error) {
	var val interface{}
	if target != nil {
		val = *target
	}
	res := r.db.Model(&memorydomain.Matter{}).
		Where("user_id = ? AND id = ?", userID, id).
		Update("target_at", val)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
