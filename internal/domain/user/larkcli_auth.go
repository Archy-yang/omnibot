package user

import "time"

// LarkCLI 授权状态(19-多用户LarkCLI授权技术方案 §3)。
const (
	LarkAuthStatusPending    = "pending"    // 已发起,等待用户浏览器确认
	LarkAuthStatusAuthorized = "authorized" // 授权完成(收尾成功)
	LarkAuthStatusFailed     = "failed"     // 收尾失败(device code 过期/拒绝),可重新发起
)

// LarkCLIAuth 用户级 lark-cli 授权:DB 存"路由与状态",token 本体留在 lark-cli 自有存储
// (.enc 按 profile 隔离,CLI 自管刷新——19 号 §1.1 决策)。
type LarkCLIAuth struct {
	ID           int64  `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID       int64  `gorm:"uniqueIndex;not null" json:"user_id"`         // 一个用户一份授权(v1)
	ProfileName  string `gorm:"uniqueIndex;size:64;not null" json:"profile_name"` // "user-<id>",可由 user_id 推导
	AppID        string `gorm:"size:64;not null" json:"app_id"`
	AppSecretEnc string `gorm:"size:1024;not null" json:"-"`                 // AES 密文(enc:),仅 profile 创建时解密;永不回显
	OpenID       string `gorm:"size:64" json:"open_id"`
	UserName     string `gorm:"size:128" json:"user_name"`
	Status       string `gorm:"size:16;not null;default:pending" json:"status"`
	DeviceCode   string `gorm:"size:128" json:"-"`                           // pending 期间短时保存,收尾即清
	VerifiedURL  string `gorm:"size:512" json:"-"`                           // pending 期间验证地址(经 view 定向返回)
	CreatedAt    time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt    time.Time `gorm:"not null" json:"updated_at"`
}

func (LarkCLIAuth) TableName() string { return "user_larkcli_auths" }
