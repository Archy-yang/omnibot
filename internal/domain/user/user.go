package user

import (
	"time"
)

// 用户状态常量
const (
	StatusNormal  = int8(0)
	StatusBanned  = int8(1)
	StatusDeleted = int8(2)
)

// User 核心用户实体
type User struct {
	ID            int64      `gorm:"primaryKey;autoIncrement"`
	Phone         *string    `gorm:"size:20;unique"`
	PhoneVerified bool       `gorm:"default:false"`
	PhoneBindTime *time.Time
	Status        int8       `gorm:"default:0;not null"` // 0-正常, 1-封禁, 2-删除
	CreatedAt     time.Time  `gorm:"not null"`
	UpdatedAt     time.Time  `gorm:"not null"`
}

// NewUser 创建新用户
func NewUser() *User {
	now := time.Now()
	return &User{
		Status:    StatusNormal,
		CreatedAt: now,
		UpdatedAt: now,
	}
}

// BindPhone 绑定手机号
func (u *User) BindPhone(phone string) {
	u.Phone = &phone
	u.PhoneVerified = true
	now := time.Now()
	u.PhoneBindTime = &now
	u.UpdatedAt = time.Now()
}

// Ban 封禁用户
func (u *User) Ban() {
	u.Status = StatusBanned
	u.UpdatedAt = time.Now()
}

// Unban 解封用户
func (u *User) Unban() {
	u.Status = StatusNormal
	u.UpdatedAt = time.Now()
}

// SoftDelete 软删除用户
func (u *User) SoftDelete() {
	u.Status = StatusDeleted
	u.UpdatedAt = time.Now()
}
