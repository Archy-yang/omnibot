package memory

import "time"

// MemoryHit 带相关度分数的记忆检索命中(12-记忆系统技术方案 §6.4)。
type MemoryHit struct {
	Memory *Memory
	Score  float64
}

// MatterHit 事项检索命中(M6.2 两段式):事项(含当前状态)+ 其挂靠的原子记忆全景。
// 检索"这件事到哪了"先命中事项,整体调出,而非靠相似度碰散落的事实片段。
type MatterHit struct {
	Matter *Matter
	Facts  []*Memory // matter_id 挂靠的原子记忆(含 fact/episode/loop)
	Score  float64
}

// MessageHit 中期记忆检索命中(M7 §10.6):原文片段(回表所得,零抽象)+ 发生时间 + 融合分数。
type MessageHit struct {
	MessageID int64
	Role      string
	Content   string
	CreatedAt time.Time
	Score     float64
}
