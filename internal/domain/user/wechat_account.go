package user

import (
	"time"
)

// WechatAccount 微信账号实体
// 一个 User 可以关联多个 WechatAccount（不同公众号/小程序）
type WechatAccount struct {
	ID        int64      `gorm:"primaryKey;autoIncrement"`
	UserID    int64      `gorm:"not null;index"`
	OpenID    string     `gorm:"size:128;not null;unique"`
	UnionID   *string    `gorm:"size:128;unique"`
	AppID     string     `gorm:"size:64"` // 来源公众号/小程序 AppID
	CreatedAt time.Time  `gorm:"not null"`
	UpdatedAt time.Time  `gorm:"not null"`

	User User `gorm:"foreignKey:UserID"`
}

// NewWechatAccount 创建新微信账号
func NewWechatAccount(userID int64, openID string) *WechatAccount {
	now := time.Now()
	return &WechatAccount{
		UserID:    userID,
		OpenID:    openID,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// SetUnionID 设置 UnionID
func (w *WechatAccount) SetUnionID(unionID string) {
	w.UnionID = &unionID
	w.UpdatedAt = time.Now()
}
