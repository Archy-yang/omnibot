// Package wechatclaw 提供微信 ClawBot 账号凭证的仓储实现。
package wechatclaw

import (
	"time"

	"gorm.io/gorm"

	domain "omnibot/internal/domain/wechatclaw"
)

// AccountRepository 凭证仓储(单账号语义:Get 返回最新一条,Save 覆盖)。
type AccountRepository interface {
	// Get 返回最新一条账号记录;无记录返回 (nil, nil)。
	Get() (*domain.Account, error)
	// Save 写入账号:无 ID 时 Create,有 ID 时整行覆盖(重新扫码 = 覆盖旧凭证)。
	Save(account *domain.Account) error
	// UpdateSyncBuf 更新长轮询游标(每轮都要写,单独方法避免整行更新)。
	UpdateSyncBuf(id int64, buf string) error
	// UpdateStatus 更新账号状态(active / expired)。
	UpdateStatus(id int64, status string) error
	// DeleteAll 清空账号(断开连接)。
	DeleteAll() error
}

// GormAccountRepository GORM 实现。
type GormAccountRepository struct {
	db *gorm.DB
}

// NewAccountRepository 创建仓储。
func NewAccountRepository(db *gorm.DB) AccountRepository {
	return &GormAccountRepository{db: db}
}

// Get 取最新一条记录。用 Limit+Find 而不是 First:无记录属正常状态(未登录),
// First 会触发 GORM "record not found" 日志,污染启动日志。
func (r *GormAccountRepository) Get() (*domain.Account, error) {
	var accounts []domain.Account
	if err := r.db.Order("id DESC").Limit(1).Find(&accounts).Error; err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, nil
	}
	return &accounts[0], nil
}

// Save 新建或整行覆盖。
func (r *GormAccountRepository) Save(account *domain.Account) error {
	now := time.Now()
	account.UpdatedAt = now
	if account.ID == 0 {
		if account.CreatedAt.IsZero() {
			account.CreatedAt = now
		}
		return r.db.Create(account).Error
	}
	return r.db.Save(account).Error
}

// UpdateSyncBuf 更新游标。
func (r *GormAccountRepository) UpdateSyncBuf(id int64, buf string) error {
	return r.db.Model(&domain.Account{}).
		Where("id = ?", id).
		Updates(map[string]any{"sync_buf": buf, "updated_at": time.Now()}).Error
}

// UpdateStatus 更新状态。
func (r *GormAccountRepository) UpdateStatus(id int64, status string) error {
	return r.db.Model(&domain.Account{}).
		Where("id = ?", id).
		Updates(map[string]any{"status": status, "updated_at": time.Now()}).Error
}

// DeleteAll 清空。
func (r *GormAccountRepository) DeleteAll() error {
	return r.db.Where("1 = 1").Delete(&domain.Account{}).Error
}
