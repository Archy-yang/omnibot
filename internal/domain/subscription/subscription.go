package subscription

import "time"

// Subscription 订阅源登记簿(14-订阅源管理技术方案 §4)。
// 定位:程序消费的结构化实体——供 Agent 枚举(选源)与去重(防重订);
// 叙事层由 M6 沉淀管线自然生成记忆,与本表不互相同步(§7)。
// 无抓取水位/内容字段:查询时按需现抓(§1.3 非目标:不做定时抓取)。
type Subscription struct {
	ID         int64     `gorm:"primaryKey" json:"id"`
	UserID     int64     `gorm:"not null;uniqueIndex:idx_user_feed,priority:1" json:"user_id"`
	SiteURL    string    `gorm:"size:500;not null" json:"site_url"`                                      // 用户给的网站/博客地址
	FeedURL    string    `gorm:"size:500;not null;uniqueIndex:idx_user_feed,priority:2" json:"feed_url"` // 发现并验证的 feed 地址
	Title      string    `gorm:"size:200;not null" json:"title"`                                         // feed 自带标题
	TopicDesc  string    `gorm:"size:500" json:"topic_desc"`                                             // 主题描述(选源依据,订阅时生成)
	Status     string    `gorm:"size:20;not null;default:active" json:"status"`                          // active / paused
	CreatedVia string    `gorm:"size:20;not null;default:manual" json:"created_via"`                     // manual / auto(预留:自动建议)
	CreatedAt  time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt  time.Time `gorm:"not null" json:"updated_at"`
}

func (Subscription) TableName() string { return "subscriptions" }

// 订阅状态与来源枚举(工具与提示词共用语义)
const (
	StatusActive = "active"
	StatusPaused = "paused"

	ViaManual = "manual"
	ViaAuto   = "auto" // 预留:反复强调→自动建议(§7)
)

// FeedInfo 自动发现并验证通过的信息源(14 §5)。
type FeedInfo struct {
	FeedURL     string
	Title       string
	Description string
}

// AddResult 订阅结果:Subscription 与 Candidates 互斥——成功入库给前者,
// 站点有多个 feed 时给后者(宁漏勿错:不擅自替用户选)。
type AddResult struct {
	Subscription *Subscription
	Candidates   []*FeedInfo
}
