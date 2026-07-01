package user

import "time"

// UserCredential 用户认证凭据(邮箱账号的密码哈希)
// 仅 email 账号有此记录;wechat/feishu 通过 UserChannels 解析身份,不走此表
type UserCredential struct {
	ID           int64     `gorm:"primaryKey;autoIncrement"`
	UserID       int64     `gorm:"not null;uniqueIndex"` // 关联 users.id,一对一
	PasswordHash string    `gorm:"size:255;not null"`    // bcrypt 哈希(含 salt),明文密码不落库
	CreatedAt    time.Time `gorm:"not null"`
	UpdatedAt    time.Time `gorm:"not null"`
}

// TableName 设置表名
func (UserCredential) TableName() string {
	return "user_credentials"
}
