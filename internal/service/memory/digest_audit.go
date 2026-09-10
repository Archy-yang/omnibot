package memory

// DigestAudit 沉淀留痕(M5.3,用户拍板"沉淀接 task/step 可观测"):
// 每轮沉淀创建一条 agent_task(type=digest,system 发起,Reported=true 静默不进回执),
// agent_step 记录 执行链(读区间 → llm_call → 落库)。nil = 不留痕(仅日志)。
// 留痕失败绝不阻断沉淀本身——记忆生产是主链路,审计是旁路。
type DigestAudit interface {
	// BeginTask 创建本轮沉淀任务并置 running。返回 taskID;失败返回 err(调用方降级为不留痕)。
	BeginTask(userID int64, fromID, toID int64, msgCount int) (int64, error)
	// RecordStep 记录一步。kind: llm_call / tool_call(tool=digest.read_range|digest.persist)。
	// request/response 存原文(llm_call 的 request 存对话原文 transcript,response 存 LLM JSON 产出)。
	RecordStep(taskID, userID int64, seq int, kind, tool, request, response, status string, durationMs int64) error
	// EndTask 收尾:status=completed(artifact=纪要全文)/failed(errorMsg)。
	EndTask(taskID int64, status, artifact, errMsg string) error
}
