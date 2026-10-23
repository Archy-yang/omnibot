package butler

import (
	"errors"
	"time"

	butlerdomain "omnibot/internal/domain/butler"

	"gorm.io/gorm"
)

// ProactiveNoteRepository 主动开口记录仓储(26 号)。
type ProactiveNoteRepository interface {
	Create(note *butlerdomain.ProactiveNote) error
	// CountDeliveredOnDay 统计自然日(自 dayStart 起 24h)内 delivered 条数——日限 1 闸门。
	// skipped 不计(克制闸门只看真正开口过的)。
	CountDeliveredOnDay(userID int64, dayStart time.Time) (int64, error)
	// HasDeliveredSince 指定 dedup_key 自 since 以来是否 delivered 过——7 天冷却闸门。
	HasDeliveredSince(userID int64, dedupKey string, since time.Time) (bool, error)
	// ListRecent 最近记录(创建时间倒序,设置页「主动关心」列表用)。
	ListRecent(userID int64, limit int) ([]*butlerdomain.ProactiveNote, error)
	// SetFeedback 反馈写入口(29号 N3):positive/negative 落值,空串=撤销;返回是否命中(归属隔离)。
	SetFeedback(userID, id int64, feedback string) (bool, error)
	// LatestFeedbackForKey 冷却消费面(29号 N3):该 dedup_key 最近一条 delivered 记录的
	// 反馈值(空=无反馈/已撤销);skipped(从未送达)不参与。
	LatestFeedbackForKey(userID int64, dedupKey string) (string, error)
}

type proactiveNoteRepository struct {
	db *gorm.DB
}

func NewProactiveNoteRepository(db *gorm.DB) ProactiveNoteRepository {
	return &proactiveNoteRepository{db: db}
}

func (r *proactiveNoteRepository) Create(note *butlerdomain.ProactiveNote) error {
	return r.db.Create(note).Error
}

func (r *proactiveNoteRepository) CountDeliveredOnDay(userID int64, dayStart time.Time) (int64, error) {
	var n int64
	err := r.db.Model(&butlerdomain.ProactiveNote{}).
		Where("user_id = ? AND status = ? AND created_at >= ? AND created_at < ?",
			userID, butlerdomain.NoteStatusDelivered, dayStart, dayStart.Add(24*time.Hour)).
		Count(&n).Error
	return n, err
}

func (r *proactiveNoteRepository) HasDeliveredSince(userID int64, dedupKey string, since time.Time) (bool, error) {
	var n int64
	err := r.db.Model(&butlerdomain.ProactiveNote{}).
		Where("user_id = ? AND dedup_key = ? AND status = ? AND created_at >= ?",
			userID, dedupKey, butlerdomain.NoteStatusDelivered, since).
		Count(&n).Error
	return n > 0, err
}

func (r *proactiveNoteRepository) ListRecent(userID int64, limit int) ([]*butlerdomain.ProactiveNote, error) {
	if limit <= 0 {
		limit = 20
	}
	var out []*butlerdomain.ProactiveNote
	err := r.db.Where("user_id = ?", userID).
		Order("created_at DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}

// SetFeedback 反馈写入口(29号 N3):单条覆盖写,空串即撤销;WHERE 带 user_id 归属隔离。
func (r *proactiveNoteRepository) SetFeedback(userID, id int64, feedback string) (bool, error) {
	res := r.db.Model(&butlerdomain.ProactiveNote{}).
		Where("user_id = ? AND id = ?", userID, id).
		Update("feedback", feedback)
	return res.RowsAffected > 0, res.Error
}

// LatestFeedbackForKey 冷却消费面(29号 N3):该 dedup_key 最近一条 delivered 记录的反馈。
// 只看 delivered(用户只能对真正送达过的消息反馈);skipped 不参与冷却延长。
func (r *proactiveNoteRepository) LatestFeedbackForKey(userID int64, dedupKey string) (string, error) {
	var note butlerdomain.ProactiveNote
	err := r.db.Where("user_id = ? AND dedup_key = ? AND status = ? AND feedback <> ''",
		userID, dedupKey, butlerdomain.NoteStatusDelivered).
		Order("created_at DESC").First(&note).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return note.Feedback, nil
}
