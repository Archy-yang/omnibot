// Package wechatclaw 定义微信 ClawBot 通道的领域实体。
//
// 31 号技术方案 §3.3:ClawBot 是「个人号 + 扫码授权 + 长轮询」形态,
// 与公众号通道(wechat)完全独立。整个部署单账号:全库最多一条 active 记录。
package wechatclaw

import "time"

// 账号状态。
const (
	// StatusActive 已登录且长轮询可用。
	StatusActive = "active"
	// StatusExpired token 失效(errcode -14),需要重新扫码。
	StatusExpired = "expired"
)

// Account 微信 ClawBot 登录凭证(单账号)。
//
// TokenCipher 是 AES-256-GCM 密文,任何路径都不得把明文 token 写库或回传前端。
type Account struct {
	ID int64 `gorm:"primaryKey;autoIncrement"`
	// BotID 微信侧下发的 ilink_bot_id,即机器人身份。
	BotID string `gorm:"size:128;not null;column:bot_id"`
	// ILinkUserID 机器人自己的 ilink 用户 id(诊断用)。
	ILinkUserID string `gorm:"size:128;column:ilink_user_id"`
	// BaseURL 登录返回的 baseurl;空时用固定域名。
	BaseURL string `gorm:"size:255;column:base_url"`
	// TokenCipher bot_token 密文。
	TokenCipher string `gorm:"type:text;not null;column:token_cipher"`
	// SyncBuf getupdates 游标,重启续传避免重放历史消息。
	SyncBuf string `gorm:"type:text;column:sync_buf"`
	// Status active / expired。
	Status string `gorm:"size:16;not null;default:active"`

	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName 表名。
func (Account) TableName() string { return "wechat_claw_accounts" }

// IsActive 是否可用。
func (a *Account) IsActive() bool { return a.Status == StatusActive }
