package memory

import "time"

// 风格与认知证据层(24号 §3):沉淀管线顺带抽取的原始观察,常态空数组、单轮上限 3 条。
// 注入默认关(20号红线:画像严禁自动注入);自动演化机制明确不做——只积累原始证据。

// 维度常量(未知输入归 DimensionTone)。
const (
	DimensionTone      = "tone"      // 语气/正式度
	DimensionLength    = "length"    // 长度/详略偏好
	DimensionFormality = "formality" // 称谓与礼节
	DimensionHumor     = "humor"     // 幽默感
	DimensionFormat    = "format"    // 格式偏好(列表/分段/表格)
	DimensionAddress   = "address"   // 称呼习惯
)

// NormalizeDimension 未知输入归 tone(容错,防模型漂移)。
func NormalizeDimension(d string) string {
	switch d {
	case DimensionTone, DimensionLength, DimensionFormality, DimensionHumor, DimensionFormat, DimensionAddress:
		return d
	default:
		return DimensionTone
	}
}

// StyleObservation 对用户表达方式的一条观察。
type StyleObservation struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID           int64     `gorm:"index;not null" json:"-"`
	Dimension        string    `gorm:"size:20;not null" json:"dimension"`
	Content          string    `gorm:"type:text;not null" json:"content"`
	SourceMessageIDs []int64   `gorm:"serializer:json" json:"-"` // 溯源经 briefs 拼装展示
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
}

func (StyleObservation) TableName() string { return "style_observations" }

// CognitionChange 用户明确表达的偏好/决策变化("从 X 改为 Y"叙事)。
type CognitionChange struct {
	ID               int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	UserID           int64     `gorm:"index;not null" json:"-"`
	Content          string    `gorm:"type:text;not null" json:"content"`
	SourceMessageIDs []int64   `gorm:"serializer:json" json:"-"`
	CreatedAt        time.Time `gorm:"not null" json:"created_at"`
}

func (CognitionChange) TableName() string { return "cognition_changes" }
