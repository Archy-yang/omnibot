package memory

// MemoryHit 带相关度分数的记忆检索命中(12-记忆系统技术方案 §6.4)。
type MemoryHit struct {
	Memory *Memory
	Score  float64
}

// DigestHit 带相关度分数的纪要检索命中(摘要 + 溯源区间在 Digest 内)。
type DigestHit struct {
	Digest *ConversationDigest
	Score  float64
}
