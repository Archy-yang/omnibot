package user

import "time"

// BindCode 账号绑定码(v2.2 飞书引入,v2.3 泛化为渠道通用)。
//
// 已登录 web 用户在设置里生成一个 6 位数字绑定码,在任意渠道(飞书/微信)发送
// 「绑定 <码>」完成该渠道 open_id 与 web 账号的关联。绑定成功后该码即删;5 分钟过期。
// 码本身不区分渠道--在哪个渠道发就绑哪个渠道(通用码)。
//
// user_id 唯一索引:一个账号同时只有一个有效码,重新生成走 upsert 覆盖,
// 旧码自然作废(PRD 4.1)。code 字段建普通索引,渠道端按 code 查。
type BindCode struct {
	ID        int64     `gorm:"primaryKey;autoIncrement"`
	UserID    int64     `gorm:"not null;uniqueIndex"`             // 生成该码的 web 账号;唯一
	Code      string    `gorm:"size:6;not null;index"`            // 6 位数字 000000~999999
	ExpiresAt time.Time `gorm:"not null"`                         // 过期时间(创建时 +5min)
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}

// TableName 设置表名
func (BindCode) TableName() string {
	return "bind_codes"
}

// IsExpired 判定码是否已过期
func (c *BindCode) IsExpired(now time.Time) bool {
	return now.After(c.ExpiresAt)
}
