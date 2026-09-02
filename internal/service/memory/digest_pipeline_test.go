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
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 沉淀管线测试(12-记忆系统技术方案 §7 / TDD#6/#7/#8/#9)。
// §7 修订:纪要与提取合并为单次 LLM 调用({summary, memories[]});
// 调用失败或 schema 非法 → 整批作废,水位不动,下轮重试同一区间。

// fakePipelineLLM 假 LLM:单次调用返回固定结果。
type fakePipelineLLM struct {
	mu      sync.Mutex
	resp    string
	respErr error
	calls   int
}

func (f *fakePipelineLLM) Complete(_ context.Context, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
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
	if err := db.AutoMigrate(&conversation.Message{}, &memorydomain.ConversationDigest{}, &memorydomain.DigestWatermark{}, &memorydomain.Memory{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	// 默认返回:纪要 + 无记忆候选
	llm := &fakePipelineLLM{resp: `{"summary":"纪要内容","memories":[]}`}
	sourceMsgs = nil
	source := &fakeConversationSource{latest: 0}
	p := NewDigestPipeline(
		memoryrepo.NewWatermarkRepository(db),
		memoryrepo.NewDigestRepository(db),
		memoryrepo.NewMemoryRepository(db),
		source,
		llm,
		nil, // embedding: 无向量也能落纪要
		3,   // threshold
	)
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

// TestDigestPipeline_SingleCall 阈值到 → 单次 LLM 调用产出纪要落库 + 水位推进(TDD#7)。
func TestDigestPipeline_SingleCall(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3

	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if llm.calls != 1 {
		t.Fatalf("应只调 1 次 LLM(纪要+提取合并), got %d", llm.calls)
	}
	var digests []*memorydomain.ConversationDigest
	db.Find(&digests)
	if len(digests) != 1 {
		t.Fatalf("digest count = %d, want 1", len(digests))
	}
	d := digests[0]
	if d.Status != memorydomain.DigestStatusActive {
		t.Errorf("status = %q, want active", d.Status)
	}
	if d.FromMessageID != 1 || d.ToMessageID != 3 {
		t.Errorf("区间 = [%d,%d], want [1,3]", d.FromMessageID, d.ToMessageID)
	}
	if d.MsgCount != 3 {
		t.Errorf("MsgCount = %d, want 3", d.MsgCount)
	}
	if d.Summary != "纪要内容" {
		t.Errorf("Summary = %q", d.Summary)
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
	if wm.LastDigestMsgID != 0 {
		t.Errorf("LLM 失败水位不应推进, got %d", wm.LastDigestMsgID)
	}

	// 恢复后重试:同一区间 [1,3]
	llm.mu.Lock()
	llm.respErr = nil
	llm.mu.Unlock()
	if err := p.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("retry: %v", err)
	}
	var d memorydomain.ConversationDigest
	db.First(&d)
	if d.FromMessageID != 1 || d.ToMessageID != 3 {
		t.Errorf("重试区间 = [%d,%d], want [1,3]", d.FromMessageID, d.ToMessageID)
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

// TestExtractMemories_ValidSchema 有效候选 → auto 记忆落库,带溯源与向量。
func TestExtractMemories_ValidSchema(t *testing.T) {
	p, db, llm, source := pipelineSetup(t)
	seedPipelineMessages(t, db, 42, 3)
	source.latest = 3
	llm.resp = `{"summary":"纪要","memories":[{"content":"用户偏好简洁回复","source_message_id":2}]}`
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
	llm.resp = `{"summary":"纪要","memories":[{"content":"用户喜欢简洁的回复","source_message_id":1}]}`
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
	llm.resp = `{"summary":"纪要","memories":[{"content":"用户现在住在北京","source_message_id":1}]}`
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
	llm.resp = `{"summary":"纪要","memories":[{"content":"用户是后端工程师","source_message_id":1}]}`
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
		`{"summary":"纪要","memories":[{"content":"","source_message_id":1},{"content":"%s","source_message_id":1},{"content":"有效记忆","source_message_id":99}]}`,
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
