package memory

import "time"

// 记忆来源(12-记忆系统技术方案 §4.1)
const (
	MemorySourceManual = "manual" // 用户显式交代(#记住 / Web 创建)
	MemorySourceAuto   = "auto"   // 沉淀管线自动提取
)

// 记忆分层(M6,助理人视角):原子记忆的三个种类
const (
	MemoryKindFact    = "fact"    // 稳定事实(身份/偏好/资产/家庭)
	MemoryKindEpisode = "episode" // 经历片段(具体发生过的事,带时间感,可淡忘)
	MemoryKindLoop    = "loop"    // 未决/承诺(待办钩子)
)

// loop 生命周期(M8 §14.2.2):仅 Kind=loop 语义使用;其他 kind 恒为 open。
// 列名 loop_status 刻意避开 matters.status(active/done/archived)——同名不同域。
const (
	MemoryLoopStatusOpen   = "open"
	MemoryLoopStatusClosed = "closed"
)

type Memory struct {
	ID              int64     `gorm:"primaryKey;autoIncrement"`
	UserID          int64     `gorm:"index;not null"`
	Content         string    `gorm:"type:text;not null"`
	Source          string    `gorm:"size:20;not null;default:manual"` // manual/auto
	Kind            string    `gorm:"size:20;not null;default:fact"`   // M6 分层:fact/episode/loop
	LoopStatus      string    `gorm:"size:20;not null;default:open"`   // M8:loop 生命周期 open/closed(仅 kind=loop 有意义)
	SourceMessageID *int64    // 溯源:首个来源消息 ID(主指针,展示兼容;完整映射见 memory_message_links)
	MatterID        *int64    `gorm:"index"`           // M6:可选挂靠事项;独立事实/闲聊经历为 NULL
	Embedding       []float32 `gorm:"serializer:json"` // JSON 向量列,SQLite/PG 通吃;NULL=未嵌入(检索走子串降级)
	EmbeddingModel  string    `gorm:"size:100"`        // 生成向量的模型标识,检索只比同模型向量(§6.3)
	Category        string     `gorm:"size:50"` // 预留列,本期恒空
	Importance      int        // 预留列,本期恒 0
	Pinned          bool       `gorm:"not null;default:false"` // M8.3 §14.2.4:常驻 core 人工置顶(manual ∪ pinned auto 进常驻注入)
	PinnedAt        *time.Time // 置顶时间(取消置顶置 NULL;截断按 pinned_at DESC 新近优先)
	CreatedAt       time.Time  `gorm:"not null"`
	UpdatedAt       time.Time  `gorm:"not null"`
}

func (Memory) TableName() string {
	return "memories"
}

// NormalizeKind 归一记忆分层:非法/空 → fact。
// M8.4 §14.2.3:episode 经历层被 conversation_chunks 取代,沉淀断源;
// MemoryKindEpisode 保留为历史兼容值,显式归一为 fact(调用侧打 warn,不静默)。
func NormalizeKind(s string) string {
	switch s {
	case MemoryKindLoop:
		return s
	case MemoryKindEpisode:
		return MemoryKindFact
	default:
		return MemoryKindFact
	}
}

// NormalizeLoopStatus 归一 loop 生命周期:非法/空 → open。
func NormalizeLoopStatus(s string) string {
	switch s {
	case MemoryLoopStatusClosed:
		return s
	default:
		return MemoryLoopStatusOpen
	}
}

// IsClosedLoop 该记忆是否为已关闭的 loop(检索过滤/去重链裁决用)。
func (m *Memory) IsClosedLoop() bool {
	return m.Kind == MemoryKindLoop && m.LoopStatus == MemoryLoopStatusClosed
}

// NewMemory 创建显式记忆(Source=manual,无溯源指针)。
func NewMemory(userID int64, content string) *Memory {
	now := time.Now()
	return &Memory{
		UserID:     userID,
		Content:    content,
		Source:     MemorySourceManual,
		LoopStatus: MemoryLoopStatusOpen,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
}

// NewAutoMemory 创建自动沉淀记忆(Source=auto,带来源消息指针)。
// kind/matterID 由沉淀对账填充(M6);此处给缺省 kind=fact。
func NewAutoMemory(userID int64, content string, sourceMessageID *int64) *Memory {
	now := time.Now()
	return &Memory{
		UserID:          userID,
		Content:         content,
		Source:          MemorySourceAuto,
		Kind:            MemoryKindFact,
		LoopStatus:      MemoryLoopStatusOpen,
		SourceMessageID: sourceMessageID,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}
