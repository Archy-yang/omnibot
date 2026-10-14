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
	ID              int64      `gorm:"primaryKey;autoIncrement"`
	UserID          int64      `gorm:"index;index:idx_user_happened,priority:1;not null"`
	Content         string     `gorm:"type:text;not null"`
	Source          string     `gorm:"size:20;not null;default:manual"` // manual/auto
	Kind            string     `gorm:"size:20;not null;default:fact"`   // M6 分层:fact/loop(episode 为历史兼容值,M8.4 起断源并归一为 fact)
	LoopStatus      string     `gorm:"size:20;not null;default:open"`   // M8:loop 生命周期 open/closed(仅 kind=loop 有意义)
	SourceMessageID *int64     // 溯源:首个来源消息 ID(主指针,展示兼容;完整映射见 memory_message_links)
	MatterID        *int64     `gorm:"index"`           // M6:可选挂靠事项;独立事实/闲聊经历为 NULL
	HappenedAt      *time.Time `gorm:"index:idx_user_happened,priority:2"` // 21号:事件发生时间(NULL=未识别,展示回落 CreatedAt);抽取见 digest_extract.parseHappenedAt
	Embedding       []float32  `gorm:"serializer:json"` // JSON 向量列,SQLite/PG 通吃;NULL=未嵌入(检索走子串降级)
	EmbeddingModel  string     `gorm:"size:100"`        // 生成向量的模型标识,检索只比同模型向量(§6.3)
	Category        string     `gorm:"size:50"`         // 预留列,本期恒空
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

// MemoryInjection 常驻注入数据(§6.5 注入分层 + M8.3 §14.2.4)。
// 领域值对象:memory service 产出、chat service 消费(经 MemoryInjectionProvider
// 接口)——定义在 domain 层,消费方不必依赖 memory service 实现包(架构复评 P1-4)。
// MemoryFilter 检索过滤(22号 §3):时间锚=有效时间(happened_at ?? created_at,与 Journal 同源)。
// nil 字段不过滤;nil filter 整体=无过滤(现行为)。
type MemoryFilter struct {
	From     *time.Time // 有效时间下界(含)
	To       *time.Time // 有效时间上界(含)
	MatterID *int64     // 只看某档案挂靠的记忆
}

type MemoryInjection struct {
	Manual     []string // 手动记忆全量(用户主动交代)
	PinnedAuto []string // 置顶自动记忆(常驻 core 例外,新近置顶优先)
	AutoCount  int      // 自动记忆总数
}
