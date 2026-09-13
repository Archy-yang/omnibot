package subscription

import (
	"fmt"

	"gorm.io/gorm"

	subscriptiondomain "omnibot/internal/domain/subscription"
)

// SubscriptionRepository 订阅源登记簿仓储(14-订阅源管理技术方案 §4)。
type SubscriptionRepository interface {
	// Create 新增订阅;(user_id, feed_url) 唯一索引防重订,冲突原样上抛。
	Create(sub *subscriptiondomain.Subscription) error
	// GetByID 按 id 取,带用户隔离(别的用户的订阅取不到)。
	GetByID(userID, id int64) (*subscriptiondomain.Subscription, error)
	// GetByUserAndFeed 按用户+feed 查重(幂等订阅语义用)。
	GetByUserAndFeed(userID int64, feedURL string) (*subscriptiondomain.Subscription, error)
	// ListByUserID 用户全部订阅;includePaused=false 时排除 paused(选源跳过)。
	ListByUserID(userID int64, includePaused bool) ([]*subscriptiondomain.Subscription, error)
	// UpdateStatus 暂停/恢复。
	UpdateStatus(userID, id int64, status string) error
	// Delete 删除。
	Delete(userID, id int64) error
}

type subscriptionRepository struct {
	db *gorm.DB
}

func NewSubscriptionRepository(db *gorm.DB) SubscriptionRepository {
	return &subscriptionRepository{db: db}
}

func (r *subscriptionRepository) Create(sub *subscriptiondomain.Subscription) error {
	return r.db.Create(sub).Error
}

func (r *subscriptionRepository) GetByID(userID, id int64) (*subscriptiondomain.Subscription, error) {
	var out subscriptiondomain.Subscription
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&out).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *subscriptionRepository) GetByUserAndFeed(userID int64, feedURL string) (*subscriptiondomain.Subscription, error) {
	var out subscriptiondomain.Subscription
	if err := r.db.Where("user_id = ? AND feed_url = ?", userID, feedURL).First(&out).Error; err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *subscriptionRepository) ListByUserID(userID int64, includePaused bool) ([]*subscriptiondomain.Subscription, error) {
	var out []*subscriptiondomain.Subscription
	q := r.db.Where("user_id = ?", userID)
	if !includePaused {
		q = q.Where("status = ?", subscriptiondomain.StatusActive)
	}
	if err := q.Order("created_at ASC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func (r *subscriptionRepository) UpdateStatus(userID, id int64, status string) error {
	res := r.db.Model(&subscriptiondomain.Subscription{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("status", status)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("订阅不存在: id=%d", id)
	}
	return nil
}

func (r *subscriptionRepository) Delete(userID, id int64) error {
	res := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&subscriptiondomain.Subscription{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("订阅不存在: id=%d", id)
	}
	return nil
}
