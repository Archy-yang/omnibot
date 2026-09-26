package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 沉淀管线测试(12-记忆系统技术方案 §7 / TDD#6/#7/#8/#9)。
// §7 修订:纪要与提取合并为单次 LLM 调用({summary, memories[]});
// 调用失败或 schema 非法 → 整批作废,水位不动,下轮重试同一区间。

// fakePipelineLLM 假 LLM:单次调用返回固定结果(M5.1:签名带 userID,管线按用户解析)。
type fakePipelineLLM struct {
	mu         sync.Mutex
	resp       string
	respErr    error
	calls      int
	lastUserID int64
}

func (f *fakePipelineLLM) Complete(_ context.Context, userID int64, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastUserID = userID
	if f.respErr != nil {
		return "", f.respErr
	}
	return f.resp, nil
}

// fakeConversationSource 假消息源:区间查询遍历全局种子切片 sourceMsgs(fake 不读 DB)。
type fakeConversationSource struct {
	latest int64
}

func (f *fakeConversationSource) GetLatestMessageID(_ int64) (int64, error) {
	return f.latest, nil
}

func (f *fakeConversationSource) GetRangeByUserID(_ int64, afterID, toID int64) ([]*conversation.Message, error) {
	var out []*conversation.Message
	for _, m := range sourceMsgs {
		if m.ID > afterID && m.ID <= toID {
			out = append(out, m)
		}
	}
	return out, nil
}

// sourceMsgs 种子消息切片(每个测试在 pipelineSetup 中重置)。
var sourceMsgs []*conversation.Message

func pipelineSetup(t *testing.T) (*DigestPipeline, *gorm.DB, *fakePipelineLLM, *fakeConversationSource) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&conversation.Message{}, &memorydomain.ConversationDigest{}, &memorydomain.DigestWatermark{}, &memorydomain.Memory{}, &memorydomain.MemoryMessageLink{}, &memorydomain.Matter{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// 默认返回:纪要 + 无记忆候选
	llm := &fakePipelineLLM{resp: `{"matter_updates":[],"facts":[]}`}
	sourceMsgs = nil
	source := &fakeConversationSource{latest: 0}
	p := NewDigestPipeline(
		memoryrepo.NewWatermarkRepository(db),
		memoryrepo.NewDigestRepository(db),
		memoryrepo.NewMemoryRepository(db),
		memoryrepo.NewMatterRepository(db),
		source,
		llm,
		nil, // embedding: 无向量也能落纪要
		3,   // threshold
		0,   // silenceGap:0 → 默认 10m
	)
	p.silenceGap = 0 // 本套测试聚焦对账逻辑,关闭切分边界(切分行为见 M8.1 专属测试)
	return p, db, llm, source
}

func seedPipelineMessages(t *testing.T, db *gorm.DB, userID int64, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		m := &conversation.Message{
			UserID:  userID,
			Role:    "user",
			Content: fmt.Sprintf("消息%d", i+1),
		}
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("seed message: %v", err)
		}
		sourceMsgs = append(sourceMsgs, m)
	}
}

// TestDigestPipeline_BelowThreshold 阈值未到 → 完全 no-op。
func TestDigestPipeline_BelowThreshold(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 2) // < threshold 3
	source.latest = 2

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if llm.calls != 0 {
		t.Errorf("阈值未到不应调 LLM, got %d 次", llm.calls)
	}
	var digestCount, wmCount int64
	db.Model(&memorydomain.ConversationDigest{}).Count(&digestCount)
	db.Model(&memorydomain.DigestWatermark{}).Count(&wmCount)
	if digestCount != 0 || wmCount != 0 {
		t.Errorf("digests=%d watermarks=%d, want 0/0", digestCount, wmCount)
	}
}

// TestDigestPipeline_SingleCall 阈值到 → 单次 LLM 对账 + 水位推进(M6:digests 退役不再写入)。
func TestDigestPipeline_SingleCall(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("应只调 1 次 LLM(对账), got %d", llm.calls)
	}
	if llm.lastUserID != 42 {
		t.Errorf("LLM 调用应携带 userID=42(按用户解析配置), got %d", llm.lastUserID)
	}
	// M6:digests 退役,不再写入
	var digestCount int64
	db.Model(&memorydomain.ConversationDigest{}).Count(&digestCount)
	if digestCount != 0 {
		t.Errorf("digests 已退役,不应写入, got %d", digestCount)
	}

	// 水位推进到 3
	wm, _ := memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm.LastDigestMsgID != 3 {
		t.Errorf("watermark = %d, want 3", wm.LastDigestMsgID)
	}

	// 下轮:没有新消息,不再调 LLM
	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce again: %v", err)
	}
	if llm.calls != 1 {
		t.Error("无新消息不应再触发沉淀")
	}
}

// TestDigestPipeline_NoLLMForUser_SkipsQuietly 用户无可用 LLM 配置(ErrPipelineNoLLM)
// → 静默跳过本轮(RunOnce 返回 nil,不告警刷屏);水位不动,配置后自动从当前区间开始。
func TestDigestPipeline_NoLLMForUser_SkipsQuietly(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.respErr = ErrPipelineNoLLM

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("无配置应是静默跳过(nil), got %v", err)
	}
	// 水位未推进(配置 LLM 后从同一区间开始沉淀)
	wm, _ := memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm != nil && wm.LastDigestMsgID != 0 {
		t.Errorf("水位不应推进, got %d", wm.LastDigestMsgID)
	}
}

// TestDigestPipeline_ChunkedBacklog 积压超过单块上限 → 每轮只沉淀一块,水位分块推进
// (M5.1 硬化:首次沉淀/长期停用后的全量积压不再一个巨包打给 LLM)。
func TestDigestPipeline_ChunkedBacklog(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 10)
	source.latest = 10
	p.maxBatchMessages = 4

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce#1: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("第一轮应只调 1 次 LLM, got %d", llm.calls)
	}
	wmRepo := memoryrepo.NewWatermarkRepository(db)
	wm, _ := wmRepo.GetByUserID(42)
	if wm.LastDigestMsgID != 4 {
		t.Fatalf("水位应推进到第 4 条, got %d", wm.LastDigestMsgID)
	}

	// 第二轮:下一块
	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce#2: %v", err)
	}
	wm, _ = wmRepo.GetByUserID(42)
	if wm.LastDigestMsgID != 8 {
		t.Fatalf("第二轮水位应到 8, got %d", wm.LastDigestMsgID)
	}

	// 第三轮:剩余 2 条 < 阈值 3 → 攒着,并入下一批(不单独为尾巴起一轮)
	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce#3: %v", err)
	}
	wm, _ = wmRepo.GetByUserID(42)
	if wm.LastDigestMsgID != 8 {
		t.Fatalf("尾巴不足阈值应攒到下批, 水位应仍为 8, got %d", wm.LastDigestMsgID)
	}
	if llm.calls != 2 {
		t.Errorf("共应调 2 次 LLM, got %d", llm.calls)
	}
}

// TestExtractMemories_MultiSourceLinks 多溯源(M5.2):候选带多条依据消息
// → memory_message_links 只留区间内合法 ID;Memory.SourceMessageID 取首个作主指针。
func TestExtractMemories_MultiSourceLinks(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	// 依据 = 消息 1+2 交叉得出;99 越界应被丢弃
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户偏好简洁回复","kind":"fact","source_message_ids":[1,2,99]}]}`

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	require.Len(t, mems, 1)
	require.NotNil(t, mems[0].SourceMessageID)
	require.Equal(t, int64(1), *mems[0].SourceMessageID) // 首个合法来源作主指针

	var linkMsgIDs []int64
	db.Model(&memorydomain.MemoryMessageLink{}).Where("memory_id = ?", mems[0].ID).
		Order("message_id").Pluck("message_id", &linkMsgIDs)
	require.Equal(t, []int64{1, 2}, linkMsgIDs) // 越界 99 被过滤
}

// TestExtractMemories_ConflictUpdate_ReplacesLinks 疑似冲突原位更新(M5.2):
// 新事实依据的消息变了 → 溯源映射整体替换。
func TestExtractMemories_ConflictUpdate_ReplacesLinks(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 4)
	source.latest = 4
	// 既有记忆("用户住在上海",依据消息 1):旧向量 (0.85,0.5268,0) 与候选 (1,0,0) 余弦 ≈0.85 ∈ [0.80,0.92)
	emb := &fakeEmbedding{vectors: map[string][]float32{
		"用户现在住在北京": {1, 0, 0},
	}, name: "fake/m1"}
	p.embedding = emb
	oldMem := &memorydomain.Memory{
		UserID: 42, Content: "用户住在上海", Source: memorydomain.MemorySourceAuto,
		Embedding:      []float32{0.85, 0.5268, 0},
		EmbeddingModel: "fake/m1",
	}
	require.NoError(t, db.Create(oldMem).Error)
	require.NoError(t, db.Create(&memorydomain.MemoryMessageLink{MemoryID: oldMem.ID, MessageID: 1}).Error)

	// 候选"用户现在住在北京"(依据消息 3、4) → 原位更新 + 链路替换
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户现在住在北京","kind":"fact","source_message_ids":[3,4]}]}`

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	require.Len(t, mems, 1, "冲突应原位更新而非新增")
	require.Equal(t, "用户现在住在北京", mems[0].Content)

	var linkMsgIDs []int64
	db.Model(&memorydomain.MemoryMessageLink{}).Where("memory_id = ?", mems[0].ID).
		Order("message_id").Pluck("message_id", &linkMsgIDs)
	require.Equal(t, []int64{3, 4}, linkMsgIDs, "溯源映射应整体替换为新依据")
}

// ===== 留痕(M5.3):task+step 可观测 =====

// fakeDigestAudit 捕获全部留痕调用。
type fakeDigestAudit struct {
	beginUserID, beginFrom, beginTo int64
	beginCount                      int
	beginErr                        error
	taskIDSeq                       int64

	steps []struct {
		taskID, userID, seq int64
		kind, tool, status  string
		request, response   string
	}
	ends []struct {
		taskID           int64
		status, artifact string
		errMsg           string
	}
}

func (f *fakeDigestAudit) BeginTask(userID, fromID, toID int64, msgCount int) (int64, error) {
	if f.beginErr != nil {
		return 0, f.beginErr
	}
	f.beginUserID, f.beginFrom, f.beginTo, f.beginCount = userID, fromID, toID, msgCount
	f.taskIDSeq++
	return f.taskIDSeq, nil
}

func (f *fakeDigestAudit) RecordStep(taskID, userID int64, seq int, kind, tool, request, response, status string, _ int64) error {
	f.steps = append(f.steps, struct {
		taskID, userID, seq int64
		kind, tool, status  string
		request, response   string
	}{taskID, userID, int64(seq), kind, tool, status, request, response})
	return nil
}

func (f *fakeDigestAudit) EndTask(taskID int64, status, artifact, errMsg string) error {
	f.ends = append(f.ends, struct {
		taskID           int64
		status, artifact string
		errMsg           string
	}{taskID, status, artifact, errMsg})
	return nil
}

// TestDigestPipeline_AuditSuccess 成功轮 → 建 task + 2 步(llm_call/persist) + completed。
func TestDigestPipeline_AuditSuccess(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户偏好简洁回复","kind":"fact","source_message_ids":[2]}]}`
	audit := &fakeDigestAudit{}
	p.SetAudit(audit)

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if audit.beginUserID != 42 || audit.beginFrom != 1 || audit.beginTo != 3 || audit.beginCount != 3 {
		t.Errorf("BeginTask 参数不符: user=%d from=%d to=%d count=%d",
			audit.beginUserID, audit.beginFrom, audit.beginTo, audit.beginCount)
	}
	if len(audit.steps) != 2 {
		t.Fatalf("应记录 2 步, got %d", len(audit.steps))
	}
	if audit.steps[0].kind != "llm_call" || audit.steps[0].status != "success" {
		t.Errorf("step0 应为 llm_call/success, got %+v", audit.steps[0])
	}
	if !strings.Contains(audit.steps[0].request, "消息1") {
		t.Errorf("llm_call request 应存对话原文, got %q", audit.steps[0].request)
	}
	if audit.steps[1].tool != "digest.persist" || audit.steps[1].status != "success" {
		t.Errorf("step1 应为 digest.persist/success, got %+v", audit.steps[1])
	}
	if len(audit.ends) != 1 || audit.ends[0].status != "completed" ||
		audit.ends[0].artifact != "事项更新 0,新增记忆 1,更新记忆 0,关闭 loop 0,重开 loop 0" {
		t.Errorf("EndTask 应为 completed+对账统计, got %+v", audit.ends)
	}
}

// TestDigestPipeline_AuditLLMFailure LLM 失败 → llm_call error 步 + failed 收尾。
func TestDigestPipeline_AuditLLMFailure(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.respErr = fmt.Errorf("llm down")
	audit := &fakeDigestAudit{}
	p.SetAudit(audit)

	if err := p.RunOnce(context.Background(), 42); err == nil {
		t.Fatal("LLM 失败应返回 error")
	}
	if len(audit.steps) != 1 || audit.steps[0].status != "error" {
		t.Fatalf("应记录 1 步 llm_call/error, got %+v", audit.steps)
	}
	if len(audit.ends) != 1 || audit.ends[0].status != "failed" || !strings.Contains(audit.ends[0].errMsg, "llm down") {
		t.Errorf("EndTask 应为 failed 含错误, got %+v", audit.ends)
	}
}

// TestDigestPipeline_AuditNoLLM_NoTask 用户无 LLM → 完全不留痕(避免每轮刷 cancelled 任务)。
func TestDigestPipeline_AuditNoLLM_NoTask(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.respErr = ErrPipelineNoLLM
	audit := &fakeDigestAudit{}
	p.SetAudit(audit)

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("无配置应静默跳过, got %v", err)
	}
	if audit.taskIDSeq != 0 || len(audit.ends) != 0 {
		t.Errorf("无 LLM 不应建任务, got tasks=%d ends=%d", audit.taskIDSeq, len(audit.ends))
	}
}

// TestDigestPipeline_AuditBeginFails_Degrades 留痕建任务失败 → 降级仅日志,沉淀照常完成。
func TestDigestPipeline_AuditBeginFails_Degrades(t *testing.T) {
	p, db, _, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	audit := &fakeDigestAudit{beginErr: fmt.Errorf("audit db down")}
	p.SetAudit(audit)

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("留痕失败不应阻断沉淀, got %v", err)
	}
	wm, _ := memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm == nil || wm.LastDigestMsgID != 3 {
		t.Errorf("水位应照常推进, got %+v", wm)
	}
}

// TestDigestPipeline_LLMFailureRetriesSameRange LLM 失败 → 整批作废、水位不动,下轮重试同一区间(TDD#6)。
func TestDigestPipeline_LLMFailureRetriesSameRange(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.respErr = fmt.Errorf("llm down")

	// RunOnce 返回 error 表示"本轮未完成,水位未推进"(调用方可安全忽略,NotifyTurn 内部记日志)
	if err := p.RunOnce(context.Background(), 42); err == nil {
		t.Fatal("LLM 失败应返回 error 供调用方记录")
	}
	var digestCount int64
	db.Model(&memorydomain.ConversationDigest{}).Count(&digestCount)
	if digestCount != 0 {
		t.Errorf("LLM 失败不应落纪要, got %d", digestCount)
	}
	wm, _ := memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm != nil && wm.LastDigestMsgID != 0 {
		t.Errorf("LLM 失败水位不应推进, got %d", wm.LastDigestMsgID)
	}

	// 恢复后重试:同一区间 [1,3](水位推进即证明)
	llm.mu.Lock()
	llm.respErr = nil
	llm.mu.Unlock()
	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("retry: %v", err)
	}
	wm, _ = memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm == nil || wm.LastDigestMsgID != 3 {
		t.Errorf("重试后水位应到 3, got %+v", wm)
	}
}

// TestDigestPipeline_SchemaInvalidDropsBatch schema 非法 → 整批作废(纪要/提取都不落),水位不动(TDD#8)。
func TestDigestPipeline_SchemaInvalidDropsBatch(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `这不是JSON`

	if err := p.RunOnce(context.Background(), 42); err == nil {
		t.Fatal("schema 非法应返回 error(整批作废,下轮重试)")
	}
	var digestCount, memCount int64
	db.Model(&memorydomain.ConversationDigest{}).Count(&digestCount)
	db.Model(&memorydomain.Memory{}).Count(&memCount)
	if digestCount != 0 || memCount != 0 {
		t.Errorf("schema 非法应整批作废, digests=%d mems=%d", digestCount, memCount)
	}
	wm, _ := memoryrepo.NewWatermarkRepository(db).GetByUserID(42)
	if wm.LastDigestMsgID != 0 {
		t.Errorf("schema 非法水位不应推进, got %d", wm.LastDigestMsgID)
	}
}

// TestDigestPipeline_PerUserSingleFlight 同一用户并发触发不并发跑(串行化)。
func TestDigestPipeline_PerUserSingleFlight(t *testing.T) {
	p, db, _, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3

	// 用阻塞式 source 探测并发:进入 GetRangeByUserID 时计数,已有 in-flight 则 >1
	var concurrent, maxConcurrent int32
	p.source = &blockingSource{
		inner: source,
		onEnter: func() {
			cur := atomic.AddInt32(&concurrent, 1)
			for {
				old := atomic.LoadInt32(&maxConcurrent)
				if cur <= old || atomic.CompareAndSwapInt32(&maxConcurrent, old, cur) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt32(&concurrent, -1)
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = p.RunOnce(context.Background(), 42)
		}()
	}
	wg.Wait()
	if atomic.LoadInt32(&maxConcurrent) > 1 {
		t.Errorf("同一用户并发跑, maxConcurrent=%d, want 1", maxConcurrent)
	}
}

type blockingSource struct {
	inner   ConversationSource
	onEnter func()
}

func (b *blockingSource) GetLatestMessageID(userID int64) (int64, error) {
	return b.inner.GetLatestMessageID(userID)
}

func (b *blockingSource) GetRangeByUserID(userID int64, afterID, toID int64) ([]*conversation.Message, error) {
	b.onEnter()
	return b.inner.GetRangeByUserID(userID, afterID, toID)
}

// ===== 记忆提取落库(§7.3 / TDD#8/#9) =====

// TestPipeline_EmbeddingResolverUsed 用户级 embedding 解析器生效(M5.1):
// 管线向量化按 userID 解析,产出的纪要/记忆带该用户配置的向量。
func TestPipeline_EmbeddingResolverUsed(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户偏好简洁回复","kind":"fact","source_message_ids":[2]}]}`
	emb := &fakeEmbedding{vectors: map[string][]float32{"用户偏好简洁回复": {1, 0, 0}}, name: "fake/user-m1"}
	var gotUserID int64
	p.SetEmbeddingResolver(func(userID int64) EmbeddingProvider {
		gotUserID = userID
		return emb
	})
	p.embedding = nil // 用户解析器是唯一向量来源

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if gotUserID != 42 {
		t.Errorf("resolver 应按 userID 调用, got %d", gotUserID)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 || mems[0].EmbeddingModel != "fake/user-m1" || len(mems[0].Embedding) != 3 {
		t.Fatalf("记忆应带用户级向量, got %+v", mems)
	}
}

// TestPipeline_EmbeddingResolverFallsBack 解析器返回 nil → 回落系统默认 embedding。
func TestPipeline_EmbeddingResolverFallsBack(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户偏好简洁回复","kind":"fact","source_message_ids":[2]}]}`
	p.SetEmbeddingResolver(func(int64) EmbeddingProvider { return nil }) // 用户未配置
	p.embedding = &fakeEmbedding{vectors: map[string][]float32{"用户偏好简洁回复": {1, 0, 0}}, name: "fake/system-m1"}

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 || mems[0].EmbeddingModel != "fake/system-m1" {
		t.Fatalf("应回落系统默认向量, got %+v", mems)
	}
}

// TestExtractMemories_ValidSchema 有效候选 → auto 记忆落库,带溯源与向量。
func TestExtractMemories_ValidSchema(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户偏好简洁回复","kind":"fact","source_message_ids":[2]}]}`
	emb := &fakeEmbedding{
		vectors: map[string][]float32{"用户偏好简洁回复": {1, 0, 0}},
		name:    "fake/m1",
	}
	p.embedding = emb

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 {
		t.Fatalf("memory count = %d, want 1", len(mems))
	}
	m := mems[0]
	if m.Source != memorydomain.MemorySourceAuto {
		t.Errorf("Source = %q, want auto", m.Source)
	}
	if m.SourceMessageID == nil || *m.SourceMessageID != 2 {
		t.Errorf("SourceMessageID = %v, want 2", m.SourceMessageID)
	}
	if m.EmbeddingModel != "fake/m1" || len(m.Embedding) != 3 {
		t.Errorf("应随写随嵌: model=%q vec=%v", m.EmbeddingModel, m.Embedding)
	}
}

// TestExtractMemories_DedupeSkip 余弦 ≥0.92 视为重复 → 跳过(TDD#8)。
func TestExtractMemories_DedupeSkip(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户喜欢简洁的回复","kind":"fact","source_message_ids":[1]}]}`
	emb := &fakeEmbedding{
		vectors: map[string][]float32{
			"用户喜欢简洁的回复": {1, 0, 0},
			"用户偏好简洁回复":  {1, 0, 0}, // 与既有记忆同向 → 余弦 1.0
		},
		name: "fake/m1",
	}
	p.embedding = emb
	// 既有记忆:同模型同向量空间
	seedMemory(t, db, 42, "用户偏好简洁回复", "fake/m1", []float32{1, 0, 0})

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 {
		t.Fatalf("重复候选应跳过, memory count = %d, want 1", len(mems))
	}
	if mems[0].Content != "用户偏好简洁回复" {
		t.Errorf("既有记忆不应被改动, got %q", mems[0].Content)
	}
}

// TestExtractMemories_ConflictUpdate 余弦在 [0.80,0.92) 视为疑似冲突 → 按新更新原文(TDD#8)。
func TestExtractMemories_ConflictUpdate(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户现在住在北京","kind":"fact","source_message_ids":[1]}]}`
	emb := &fakeEmbedding{
		vectors: map[string][]float32{
			"用户现在住在北京": {1, 0, 0},
		},
		name: "fake/m1",
	}
	p.embedding = emb
	// 既有记忆:同模型,与候选(1,0,0)方向余弦恰为 0.85 ∈ [0.80,0.92)
	seedMemory(t, db, 42, "用户住在上海", "fake/m1", []float32{0.85, 0.5268, 0})

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 {
		t.Fatalf("疑似冲突应原位更新而非新增, count = %d, want 1", len(mems))
	}
	if mems[0].Content != "用户现在住在北京" {
		t.Errorf("应按新事实更新, got %q", mems[0].Content)
	}
	if mems[0].EmbeddingModel != "fake/m1" || len(mems[0].Embedding) != 3 {
		t.Errorf("更新后应重新嵌入, model=%q", mems[0].EmbeddingModel)
	}
}

// TestExtractMemories_EmbedFailDegrades 候选嵌入失败 → 仅存文本落库(降级,TDD#9)。
func TestExtractMemories_EmbedFailDegrades(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户是后端工程师","kind":"fact","source_message_ids":[1]}]}`
	p.embedding = &fakeEmbedding{fail: true, name: "fake/m1"}

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var m memorydomain.Memory
	db.First(&m, "user_id = ?", 42)
	if m.Content != "用户是后端工程师" || m.Source != memorydomain.MemorySourceAuto {
		t.Errorf("嵌入失败应照常落库, got %+v", m)
	}
	if m.Embedding != nil || m.EmbeddingModel != "" {
		t.Errorf("嵌入失败应无向量, got model=%q", m.EmbeddingModel)
	}
}

// TestExtractMemories_Filters 过滤:空内容/超长/越界溯源。
func TestExtractMemories_Filters(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	long := strings.Repeat("长", 201)
	llm.resp = fmt.Sprintf(
		`{"matter_updates":[],"facts":[{"content":"","kind":"fact","source_message_ids":[1]},{"content":"%s","kind":"fact","source_message_ids":[1]},{"content":"有效记忆","kind":"fact","source_message_ids":[99]}]}`,
		long)
	p.embedding = &fakeEmbedding{vectors: map[string][]float32{"有效记忆": {1, 0, 0}}, name: "fake/m1"}

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var mems []*memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	if len(mems) != 1 || mems[0].Content != "有效记忆" {
		t.Fatalf("应只保留 1 条有效记忆, got %+v", mems)
	}
	if mems[0].SourceMessageID != nil {
		t.Errorf("越界溯源应置 NULL, got %v", *mems[0].SourceMessageID)
	}
}

// ===== M6 对账式沉淀 =====

// TestReconcile_MatterUpdateAndNew 事项覆写更新 + 新建,facts 挂靠 matter 与分层 kind。
func TestReconcile_MatterUpdateAndNew(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 4)
	source.latest = 4
	// 既有事项("十一旅行");本轮:既有事项有进展 + 新事项"电动车" + 三种 kind 的 facts
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一旅行", StateDesc: "机票已订", Status: memorydomain.MatterStatusActive,
	}))
	llm.resp = `{"matter_updates":[
		{"title":"十一旅行","state_desc":"机票别墅已订,交通倾向打车","status":"active","source_message_ids":[1,2]},
		{"title":"电动车充电台账","state_desc":"78 度电池,台账在飞书《充电记录》","status":"active","source_message_ids":[3]}
	],"facts":[
		{"content":"用户注重性价比","kind":"fact","matter_title":"十一旅行","source_message_ids":[2]},
		{"content":"9/4 在奥北森林公园充电","kind":"episode","matter_title":"电动车充电台账","source_message_ids":[3]},
		{"content":"待核实实时票价","kind":"loop","matter_title":"","source_message_ids":[4]}
	]}`

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	// 事项:2 条,既有事项 StateDesc 被覆写
	var matters []memorydomain.Matter
	db.Where("user_id = ?", 42).Order("title").Find(&matters)
	require.Len(t, matters, 2)
	mattersByID := map[string]memorydomain.Matter{}
	for _, m := range matters {
		mattersByID[m.Title] = m
	}
	require.Equal(t, "机票别墅已订,交通倾向打车", mattersByID["十一旅行"].StateDesc)
	require.Equal(t, "78 度电池,台账在飞书《充电记录》", mattersByID["电动车充电台账"].StateDesc)

	// 记忆:3 条,kind 分类正确,挂靠指向对应 matter
	var mems []memorydomain.Memory
	db.Where("user_id = ?", 42).Order("content").Find(&mems)
	require.Len(t, mems, 3)
	byContent := map[string]memorydomain.Memory{}
	for _, m := range mems {
		byContent[m.Content] = m
	}
	require.Equal(t, "fact", byContent["用户注重性价比"].Kind)
	require.NotNil(t, byContent["用户注重性价比"].MatterID)
	require.Equal(t, mattersByID["十一旅行"].ID, *byContent["用户注重性价比"].MatterID)
	require.Equal(t, "episode", byContent["9/4 在奥北森林公园充电"].Kind)
	require.Equal(t, "loop", byContent["待核实实时票价"].Kind)
	require.Nil(t, byContent["待核实实时票价"].MatterID, "matter_title 为空不应挂靠")
}

// TestReconcile_CasualChatNoMatter 闲聊块:只有 facts/独立信息,不硬造事项。
func TestReconcile_CasualChatNoMatter(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"matter_updates":[],"facts":[{"content":"用户今天心情不错","kind":"episode","matter_title":"","source_message_ids":[1]}]}`

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	var matters int64
	db.Model(&memorydomain.Matter{}).Count(&matters)
	require.Zero(t, matters, "闲聊不应硬造事项")
	var mems []memorydomain.Memory
	db.Where("user_id = ?", 42).Find(&mems)
	require.Len(t, mems, 1)
	require.Nil(t, mems[0].MatterID)
}

// TestWorldViewSnapshot 快照:含活跃事项标题与状态,不含 done/archived。
func TestWorldViewSnapshot(t *testing.T) {
	p, db, _, _ := pipelineSetup(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一旅行", StateDesc: "机票已订", Status: memorydomain.MatterStatusActive,
	}))
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "旧租房", StateDesc: "已退租", Status: memorydomain.MatterStatusDone,
	}))

	snap := p.buildWorldViewSnapshot(42)
	require.Contains(t, snap, "十一旅行")
	require.Contains(t, snap, "机票已订")
	require.NotContains(t, snap, "旧租房", "非活跃事项不进快照")
}

// ---- M8.1 延批切分(12-记忆系统技术方案 §14.2.1) ----

// msgSpec 消息种子规格:offsetSec=距基准时间的秒偏移;kind 空=普通/"report"=汇报。
type msgSpec struct {
	offsetSec int
	kind      string
}

// seedMsgsAt 按时间规格种子消息(真实落 DB,同时挂 sourceMsgs 供 fake source 读取)。
func seedMsgsAt(t *testing.T, db *gorm.DB, userID int64, specs []msgSpec) {
	t.Helper()
	base := time.Now().Add(-24 * time.Hour)
	for i, s := range specs {
		role := "user"
		if s.kind == conversation.KindReport {
			role = "assistant"
		}
		m := &conversation.Message{
			UserID:    userID,
			Role:      role,
			Kind:      s.kind,
			Content:   fmt.Sprintf("消息%d", i+1),
			CreatedAt: base.Add(time.Duration(s.offsetSec) * time.Second),
		}
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("seed message: %v", err)
		}
		sourceMsgs = append(sourceMsgs, m)
	}
}

// setupWithGap 构造指定 silenceGap 的管线(threshold=3)。
func setupWithGap(t *testing.T, gap time.Duration) (*DigestPipeline, *gorm.DB, *fakePipelineLLM, *fakeConversationSource) {
	t.Helper()
	p, db, llm, source := pipelineSetup(t)
	p.silenceGap = gap
	return p, db, llm, source
}

func TestSelectBatchCount_SilenceBoundary(t *testing.T) {
	// 5 条消息,第 4→5 条间隔 170s ≥ 60s → 批尾收在第 4 条(最后边界)
	msgs := make([]*conversation.Message, 5)
	base := time.Now()
	for i := range msgs {
		offset := time.Duration(i*10) * time.Second
		if i == 4 {
			offset = 200 * time.Second
		}
		msgs[i] = &conversation.Message{ID: int64(i + 1), CreatedAt: base.Add(offset)}
	}
	require.Equal(t, 4, selectBatchCount(msgs, 3, 40, 60*time.Second))
}

func TestSelectBatchCount_TailHang(t *testing.T) {
	// 5 条消息全部紧凑,无边界 → 尾部悬挂(0),水位不动
	msgs := make([]*conversation.Message, 5)
	base := time.Now()
	for i := range msgs {
		msgs[i] = &conversation.Message{ID: int64(i + 1), CreatedAt: base.Add(time.Duration(i*10) * time.Second)}
	}
	require.Zero(t, selectBatchCount(msgs, 3, 40, 60*time.Second))
}

func TestSelectBatchCount_BoundaryBelowThresholdHangs(t *testing.T) {
	// 唯一边界在第 2 条后(< threshold 3)→ 悬挂,不按小批收口
	msgs := make([]*conversation.Message, 5)
	base := time.Now()
	for i := range msgs {
		offset := time.Duration(i*10) * time.Second
		if i == 2 {
			offset = 300 * time.Second
		}
		if i == 3 {
			offset = 310 * time.Second
		}
		if i == 4 {
			offset = 320 * time.Second
		}
		msgs[i] = &conversation.Message{ID: int64(i + 1), CreatedAt: base.Add(offset)}
	}
	// 边界:1→2(10s),2→3(290s≥60s,批尾=2 < 3),3→4,4→4 紧凑 → 悬挂
	require.Zero(t, selectBatchCount(msgs, 3, 40, 60*time.Second))
}

func TestSelectBatchCount_CapOverridesHang(t *testing.T) {
	// 45 条紧凑消息:无边界但超出 40 上限 → 积压分块收口 40
	msgs := make([]*conversation.Message, 45)
	base := time.Now()
	for i := range msgs {
		msgs[i] = &conversation.Message{ID: int64(i + 1), CreatedAt: base.Add(time.Duration(i) * time.Second)}
	}
	require.Equal(t, 40, selectBatchCount(msgs, 3, 40, 60*time.Second))
}

func TestSelectBatchCount_CapWithBoundary(t *testing.T) {
	// 50 条,第 22→23 条有大间隔 → 收口 22(优先段落边界,而非顶满 40)
	msgs := make([]*conversation.Message, 50)
	base := time.Now()
	for i := range msgs {
		offset := time.Duration(i) * time.Second
		if i >= 22 {
			offset += 300 * time.Second
		}
		msgs[i] = &conversation.Message{ID: int64(i + 1), CreatedAt: base.Add(offset)}
	}
	require.Equal(t, 22, selectBatchCount(msgs, 3, 40, 60*time.Second))
}

func TestSelectBatchCount_ReportSkippedInGap(t *testing.T) {
	// Kind=report 的落库时间不可靠(异步回填,可晚于后续消息,§5.5),
	// 间隔判定必须只看相邻非 report 消息:u@0,u@10,report@7200,u@20 → 无边界悬挂
	base := time.Now()
	msgs := []*conversation.Message{
		{ID: 1, CreatedAt: base},
		{ID: 2, CreatedAt: base.Add(10 * time.Second)},
		{ID: 3, Kind: conversation.KindReport, CreatedAt: base.Add(2 * time.Hour)},
		{ID: 4, CreatedAt: base.Add(20 * time.Second)},
	}
	require.Zero(t, selectBatchCount(msgs, 3, 40, 60*time.Second))

	// 正例:u@0,u@10,report@15,u@300,u@310 → 非 report 相邻间隔 10→300 为边界,
	// 批尾=第 3 条(report 随前段一并收口)
	msgs2 := []*conversation.Message{
		{ID: 1, CreatedAt: base},
		{ID: 2, CreatedAt: base.Add(10 * time.Second)},
		{ID: 3, Kind: conversation.KindReport, CreatedAt: base.Add(15 * time.Second)},
		{ID: 4, CreatedAt: base.Add(300 * time.Second)},
		{ID: 5, CreatedAt: base.Add(310 * time.Second)},
	}
	require.Equal(t, 3, selectBatchCount(msgs2, 3, 40, 60*time.Second))
}

func TestDigestPipeline_TailHangNoProgress(t *testing.T) {
	// 积压 ≥ threshold 但无静默边界、未达上限 → 悬挂:不调 LLM、水位不动(§14.5 #1)
	p, db, llm, source := setupWithGap(t, time.Hour)
	seedPipelineMessages(t, db, 42, 4)
	source.latest = 4

	require.NoError(t, p.RunOnce(context.Background(), 42))
	require.Zero(t, llm.calls, "悬挂批不应调 LLM")
	var wmCount int64
	db.Model(&memorydomain.DigestWatermark{}).Count(&wmCount)
	require.Zero(t, wmCount, "悬挂批不应推进水位")
}

func TestDigestPipeline_SilenceBoundaryAdvancesWatermark(t *testing.T) {
	// 4 条消息,第 3→4 条间隔 ≥ 静默阈值 → 只沉淀前 3 条,水位=第 3 条 ID
	p, db, llm, source := setupWithGap(t, time.Minute)
	seedMsgsAt(t, db, 42, []msgSpec{
		{offsetSec: 0}, {offsetSec: 10}, {offsetSec: 20}, {offsetSec: 300},
	})
	source.latest = 4

	require.NoError(t, p.RunOnce(context.Background(), 42))
	require.Equal(t, 1, llm.calls)
	var wm memorydomain.DigestWatermark
	require.NoError(t, db.Where("user_id = ?", 42).First(&wm).Error)
	require.Equal(t, int64(3), wm.LastDigestMsgID, "水位=批尾(段落边界处)")
}

// ---- M8.2 loop 生命周期(12-记忆系统技术方案 §14.2.2) ----

// seedLoop 直接种子一条 loop 记忆,返回其 ID。
func seedLoop(t *testing.T, db *gorm.DB, userID int64, content, loopStatus string) int64 {
	t.Helper()
	m := memorydomain.NewAutoMemory(userID, content, nil)
	m.Kind = memorydomain.MemoryKindLoop
	m.LoopStatus = loopStatus
	require.NoError(t, db.Create(m).Error)
	return m.ID
}

func TestNormalizeLoopStatus(t *testing.T) {
	require.Equal(t, "open", memorydomain.NormalizeLoopStatus(""))
	require.Equal(t, "open", memorydomain.NormalizeLoopStatus("bogus"))
	require.Equal(t, "closed", memorydomain.NormalizeLoopStatus("closed"))
}

func TestReconcile_LoopClose(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	loopID := seedLoop(t, db, 42, "帮用户核实实时票价", memorydomain.MemoryLoopStatusOpen)
	llm.resp = fmt.Sprintf(`{"matter_updates":[],"facts":[],"loop_closes":[%d]}`, loopID)

	require.NoError(t, p.RunOnce(context.Background(), 42))
	var m memorydomain.Memory
	require.NoError(t, db.First(&m, loopID).Error)
	require.Equal(t, memorydomain.MemoryLoopStatusClosed, m.LoopStatus, "快照中的 loop 应被关闭")
}

func TestReconcile_LoopCloseIgnoresUnknownID(t *testing.T) {
	// 仅允许关闭快照中列出的 ID:不存在/他人的 ID 静默忽略,不报错、不动数据
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	loopID := seedLoop(t, db, 42, "帮用户订蛋糕", memorydomain.MemoryLoopStatusOpen)
	llm.resp = `{"matter_updates":[],"facts":[],"loop_closes":[999]}`

	require.NoError(t, p.RunOnce(context.Background(), 42))
	var m memorydomain.Memory
	require.NoError(t, db.First(&m, loopID).Error)
	require.Equal(t, memorydomain.MemoryLoopStatusOpen, m.LoopStatus)
}

func TestReconcile_LoopReopen(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	loopID := seedLoop(t, db, 42, "陪用户去充电桩", memorydomain.MemoryLoopStatusClosed)
	llm.resp = fmt.Sprintf(`{"matter_updates":[],"facts":[],"loop_reopens":[%d]}`, loopID)

	require.NoError(t, p.RunOnce(context.Background(), 42))
	var m memorydomain.Memory
	require.NoError(t, db.First(&m, loopID).Error)
	require.Equal(t, memorydomain.MemoryLoopStatusOpen, m.LoopStatus, "明确重新托付应重开")
}

func TestClassifyCandidate_ClosedLoopInteractions(t *testing.T) {
	// §14.2.2:①余弦≥0.92 重复提及不恢复(跳过);③[0.80,0.92)冲突更新不动 closed;
	// 对照:同分值的 open loop 仍走更新链
	vec := []float32{1, 0, 0}
	closed := &memorydomain.Memory{ID: 7, Kind: memorydomain.MemoryKindLoop, LoopStatus: memorydomain.MemoryLoopStatusClosed,
		Content: "旧承诺", Embedding: []float32{1, 0, 0}, EmbeddingModel: "m"}

	// ① 高相似:closed → skip(不恢复、不新建)
	id, action := classifyCandidate([]*memorydomain.Memory{closed}, vec, "m", "x")
	require.Equal(t, int64(7), id)
	require.Equal(t, candidateSkip, action)

	// ③ 中相似(cos≈0.85,余弦只看方向,需带角度的向量):closed 不作为冲突更新目标 → 新增
	midVec := []float32{0.85, 0.53, 0}
	mid := &memorydomain.Memory{ID: 9, Kind: memorydomain.MemoryKindLoop, LoopStatus: memorydomain.MemoryLoopStatusClosed,
		Content: "旧承诺", Embedding: midVec, EmbeddingModel: "m"}
	_, action = classifyCandidate([]*memorydomain.Memory{mid}, vec, "m", "x")
	require.Equal(t, candidateCreate, action, "closed 不得被冲突更新链改写")

	// 对照:open loop 同样 ≈0.85 分值 → 照常进更新链
	openMid := &memorydomain.Memory{ID: 10, Kind: memorydomain.MemoryKindLoop, LoopStatus: memorydomain.MemoryLoopStatusOpen,
		Content: "进行中承诺", Embedding: []float32{0.85, 0.53, 0}, EmbeddingModel: "m"}
	_, action = classifyCandidate([]*memorydomain.Memory{openMid}, vec, "m", "x")
	require.Equal(t, candidateUpdate, action, "open loop 仍走冲突更新链")
}

func TestWorldViewSnapshot_IncludesOpenLoopsExcludesClosed(t *testing.T) {
	p, db, _, _ := pipelineSetup(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一旅行", StateDesc: "机票已订", Status: memorydomain.MatterStatusActive,
	}))
	openID := seedLoop(t, db, 42, "帮用户核实实时票价", memorydomain.MemoryLoopStatusOpen)
	seedLoop(t, db, 42, "已完成的旧承诺", memorydomain.MemoryLoopStatusClosed)

	snap := p.buildWorldViewSnapshot(42)
	require.Contains(t, snap, "十一旅行")
	require.Contains(t, snap, fmt.Sprintf("#%d", openID), "未决 loop 带 ID 进快照")
	require.Contains(t, snap, "帮用户核实实时票价")
	require.NotContains(t, snap, "已完成的旧承诺", "closed 不进快照")
}

func TestWorldViewSnapshot_MattersCapped(t *testing.T) {
	// matters 快照上限 50:超出部分不列出并提示(成本护栏,§14.2.2 D7)
	p, db, _, _ := pipelineSetup(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	for i := 0; i < 53; i++ {
		require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
			UserID: 42, Title: fmt.Sprintf("事项%02d", i), StateDesc: "进行中", Status: memorydomain.MatterStatusActive,
		}))
	}
	snap := p.buildWorldViewSnapshot(42)
	require.Contains(t, snap, "事项52", "updated_at 倒序,最新在前")
	require.Contains(t, snap, "事项03", "窗口内最旧边界")
	require.NotContains(t, snap, "事项02", "超窗事项不列出")
	require.NotContains(t, snap, "事项00", "超窗事项不列出")
	require.Contains(t, snap, "另有 3 个事项未列出")
}

func TestSearchMemories_ExcludesClosedLoops(t *testing.T) {
	svc, db := retrievalSetup(t)
	seedLoop(t, db, 42, "已关闭的承诺事项", memorydomain.MemoryLoopStatusClosed)
	mOpen := memorydomain.NewAutoMemory(42, "开放中的承诺事项", nil)
	mOpen.Kind = memorydomain.MemoryKindLoop
	require.NoError(t, db.Create(mOpen).Error)

	hits, err := svc.SearchMemories(context.Background(), 42, "承诺事项", 10)
	require.NoError(t, err)
	for _, h := range hits {
		require.NotEqual(t, memorydomain.MemoryLoopStatusClosed, h.Memory.LoopStatus, "closed loop 不进检索")
	}
	require.NotEmpty(t, hits, "open loop 命中不受影响")
}
