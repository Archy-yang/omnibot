package memory

import (
	"context"
	"errors"
	"testing"
	"time"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
	chatrepo "omnibot/internal/repository/chat"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 语义检索管线测试(12-记忆系统技术方案 §6.4/§8 / TDD#3~#5)。
// FakeEmbeddingProvider 提供确定性向量,无网络依赖。

// fakeEmbedding 确定性假 provider:text→向量查表,查不到给零向量;fail=true 时 Embed 报错。
type fakeEmbedding struct {
	vectors map[string][]float32
	name    string
	fail    bool
}

func (f *fakeEmbedding) Embed(_ context.Context, texts []string) ([][]float32, error) {
	if f.fail {
		return nil, errors.New("embedding service down")
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if v, ok := f.vectors[t]; ok {
			out[i] = v
		} else {
			out[i] = make([]float32, 3)
		}
	}
	return out, nil
}
func (f *fakeEmbedding) Dim() int     { return 3 }
func (f *fakeEmbedding) Name() string { return f.name }

func retrievalSetup(t *testing.T) (MemoryService, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&memorydomain.Memory{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := memoryrepo.NewMemoryRepository(db)
	return NewMemoryService(repo, nil, nil, nil), db
}

func setEmbedding(t *testing.T, svc MemoryService, p EmbeddingProvider) {
	t.Helper()
	aware, ok := svc.(EmbeddingAware)
	if !ok {
		t.Fatal("MemoryService 实现未实现 EmbeddingAware")
	}
	aware.SetEmbeddingProvider(p)
}

func seedMemory(t *testing.T, db *gorm.DB, userID int64, content, model string, vec []float32) {
	t.Helper()
	m := memorydomain.NewMemory(userID, content)
	m.EmbeddingModel = model
	m.Embedding = vec
	if err := db.Create(m).Error; err != nil {
		t.Fatalf("seed memory: %v", err)
	}
}

// TestSearchMemories_Semantic 语义召回:同义不同词命中(向量近),无关记忆不返回。
func TestSearchMemories_Semantic(t *testing.T) {
	svc, db := retrievalSetup(t)
	fake := &fakeEmbedding{
		vectors: map[string][]float32{
			"用户偏好简洁回复": {1, 0, 0},
			"用户在上海工作":  {0, 1, 0},
			"喜欢简短回答":   {0.99, 0, 0}, // 与"简洁回复"近,与"上海"正交
		},
		name: "fake/m1",
	}
	setEmbedding(t, svc, fake)

	seedMemory(t, db, 42, "用户偏好简洁回复", "fake/m1", []float32{1, 0, 0})
	seedMemory(t, db, 42, "用户在上海工作", "fake/m1", []float32{0, 1, 0})

	hits, err := svc.SearchMemories(context.Background(), 42, "喜欢简短回答", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d hits, want 1 (仅语义相关的记忆)", len(hits))
	}
	if hits[0].Memory.Content != "用户偏好简洁回复" {
		t.Errorf("hit = %q, want 用户偏好简洁回复", hits[0].Memory.Content)
	}
	if hits[0].Score <= 0.9 {
		t.Errorf("score = %f, want > 0.9 (高余弦)", hits[0].Score)
	}
}

// TestSearchMemories_SubstringFallback provider 未配置 → 纯子串降级,记忆照常可检索(TDD#3 降级路径)。
func TestSearchMemories_SubstringFallback(t *testing.T) {
	svc, db := retrievalSetup(t)
	seedMemory(t, db, 42, "用户在上海工作", "", nil)

	hits, err := svc.SearchMemories(context.Background(), 42, "上海", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(hits) != 1 || hits[0].Memory.Content != "用户在上海工作" {
		t.Fatalf("子串降级应命中, got %+v", hits)
	}
}

// TestSearchMemories_ModelMismatch 向量模型标记不符 → 不做语义比较,子串仍有效(TDD#4)。
func TestSearchMemories_ModelMismatch(t *testing.T) {
	svc, db := retrievalSetup(t)
	setEmbedding(t, svc, &fakeEmbedding{
		vectors: map[string][]float32{"上海": {1, 0, 0}},
		name:    "fake/new-model",
	})
	// 记忆是老模型嵌入的
	seedMemory(t, db, 42, "用户在上海工作", "fake/old-model", []float32{1, 0, 0})

	hits, err := svc.SearchMemories(context.Background(), 42, "上海", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("异构模型应退化为子串仍命中, got %d", len(hits))
	}
	// 命中纯靠子串(分数远低于语义阈值),证明没有做跨模型余弦
	if hits[0].Score > 0.5 {
		t.Errorf("score = %f, 应为子串分而非语义分(跨模型向量不可比)", hits[0].Score)
	}
}

// TestSearchMemories_EmbedQueryFails 查询侧 embedding 失败 → 降级子串,不报错(TDD#9 读侧)。
func TestSearchMemories_EmbedQueryFails(t *testing.T) {
	svc, db := retrievalSetup(t)
	setEmbedding(t, svc, &fakeEmbedding{fail: true, name: "fake/m1"})
	seedMemory(t, db, 42, "用户在上海工作", "fake/m1", []float32{1, 0, 0})

	hits, err := svc.SearchMemories(context.Background(), 42, "上海", 5)
	if err != nil {
		t.Fatalf("查询 embedding 失败应降级而非报错, got %v", err)
	}
	if len(hits) != 1 || hits[0].Memory.Content != "用户在上海工作" {
		t.Fatalf("降级子串应命中, got %+v", hits)
	}
}

// TestSearchMemories_Fusion 语义+子串融合排序:两者皆命中 > 仅语义命中(TDD#5)。
func TestSearchMemories_Fusion(t *testing.T) {
	svc, db := retrievalSetup(t)
	setEmbedding(t, svc, &fakeEmbedding{
		vectors: map[string][]float32{
			"简洁": {1, 0, 0},
		},
		name: "fake/m1",
	})
	seedMemory(t, db, 42, "包含简洁二字的记忆", "fake/m1", []float32{1, 0, 0}) // 语义+子串
	seedMemory(t, db, 42, "用户偏好简短回复", "fake/m1", []float32{1, 0, 0})  // 仅语义

	hits, err := svc.SearchMemories(context.Background(), 42, "简洁", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].Memory.Content != "包含简洁二字的记忆" {
		t.Errorf("融合分应让语义+子串双命中排前, got %q", hits[0].Memory.Content)
	}
}

// TestSearchMemories_Empty 其他用户/空库 → 空结果非错误。
func TestSearchMemories_Empty(t *testing.T) {
	svc, _ := retrievalSetup(t)
	hits, err := svc.SearchMemories(context.Background(), 99, "任何", 5)
	if err != nil {
		t.Fatalf("空库不应报错: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("空库应返回 0 hits, got %d", len(hits))
	}
}

// TestSearchRecentMessages 中期检索(M7 §10.6 / 测试清单#4):
// 余弦取候选→回表→时间加权(新消息同分排前);异模型向量不参与;无 embedding 静默空。
func TestSearchRecentMessages(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&conversation.Message{}, &memorydomain.MessageEmbedding{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	embRepo := memoryrepo.NewMessageEmbeddingRepository(db)
	msgRepo := chatrepo.NewMessageRepository(db)
	svc := NewMemoryService(memoryrepo.NewMemoryRepository(db), nil, embRepo, msgRepo)

	// 无 embedding 配置:静默空,不报错
	hits, err := svc.SearchRecentMessages(context.Background(), 42, "充电", 2)
	if err != nil || hits != nil {
		t.Fatalf("无 embedding 应返回空, got %v err=%v", hits, err)
	}

	setEmbedding(t, svc, &fakeEmbedding{
		vectors: map[string][]float32{"充电花了多少钱": {1, 0, 0}},
		name:    "fake/m1",
	})

	now := time.Now()
	old40d := now.Add(-40 * 24 * time.Hour)
	msgs := []*conversation.Message{
		{ID: 1, UserID: 42, Role: "user", Content: "我充电总共花了多少钱", CreatedAt: old40d},
		{ID: 2, UserID: 42, Role: "assistant", Content: "老爷，充电总花费给您算好了", CreatedAt: now},
		{ID: 3, UserID: 42, Role: "user", Content: "聊点别的", CreatedAt: now},
	}
	for _, m := range msgs {
		if err := db.Create(m).Error; err != nil {
			t.Fatalf("seed msg %d: %v", m.ID, err)
		}
	}
	vec := []float32{1, 0, 0}
	embs := []*memorydomain.MessageEmbedding{
		{MessageID: 1, UserID: 42, Role: "user", Embedding: vec, EmbeddingModel: "fake/m1"},
		{MessageID: 2, UserID: 42, Role: "assistant", Embedding: vec, EmbeddingModel: "fake/m1"},
		// 异模型向量:不可比,不参与
		{MessageID: 3, UserID: 42, Role: "user", Embedding: vec, EmbeddingModel: "other/model"},
	}
	if err := embRepo.UpsertBatch(embs); err != nil {
		t.Fatalf("seed embs: %v", err)
	}

	hits, err = svc.SearchRecentMessages(context.Background(), 42, "充电花了多少钱", 2)
	if err != nil {
		t.Fatalf("SearchRecentMessages: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2(异模型消息被排除)", len(hits))
	}
	// 同余弦分,新消息时间加权后排前
	if hits[0].MessageID != 2 || hits[1].MessageID != 1 {
		t.Errorf("时间加权应新消息排前, got [%d %d]", hits[0].MessageID, hits[1].MessageID)
	}
	if hits[0].Content == "" || hits[0].CreatedAt.IsZero() {
		t.Errorf("命中应带回表原文与时间: %+v", hits[0])
	}
	if hits[0].Score <= hits[1].Score {
		t.Errorf("分数应降序: %v vs %v", hits[0].Score, hits[1].Score)
	}
}

// TestCosineSimilarity 余弦实现:同向=1,正交=0,反向=-1,零向量=0。
func TestCosineSimilarity(t *testing.T) {
	cases := []struct {
		a, b []float32
		want float64
	}{
		{[]float32{1, 0}, []float32{2, 0}, 1},
		{[]float32{1, 0}, []float32{0, 1}, 0},
		{[]float32{1, 0}, []float32{-1, 0}, -1},
		{[]float32{0, 0}, []float32{1, 0}, 0},
	}
	for _, c := range cases {
		got := CosineSimilarity(c.a, c.b)
		if got < c.want-1e-6 || got > c.want+1e-6 {
			t.Errorf("cos(%v,%v) = %f, want %f", c.a, c.b, got, c.want)
		}
	}
}

// TestSearchMemories_UserResolver 用户级 provider 覆盖系统默认(§5.3 / TDD#12 侧):
// resolver 对该用户返回用户级 provider → 与其模型标记匹配的向量参与语义检索;
// resolver 返回 nil 的用户 → 回落系统默认 provider。
func TestSearchMemories_UserResolver(t *testing.T) {
	svc, db := retrievalSetup(t)
	setEmbedding(t, svc, &fakeEmbedding{ // 系统默认:不同向量空间
		vectors: map[string][]float32{"上海": {0, 0, 1}},
		name:    "fake/system",
	})
	setResolver(svc, &fakeResolver{
		providers: map[int64]EmbeddingProvider{
			42: &fakeEmbedding{
				vectors: map[string][]float32{"上海": {1, 0, 0}},
				name:    "fake/user-qianfan",
			},
		},
	})

	// 用户级向量(模型标记 fake/user-qianfan):语义命中
	seedMemory(t, db, 42, "用户在上海工作", "fake/user-qianfan", []float32{1, 0, 0})

	hits, err := svc.SearchMemories(context.Background(), 42, "上海", 5)
	if err != nil {
		t.Fatalf("SearchMemories: %v", err)
	}
	if len(hits) != 1 || hits[0].Score <= 0.9 {
		t.Fatalf("用户级 provider 应语义命中, got %+v", hits)
	}

	// 无用户级配置的用户(7)回落系统默认:其模型标记 fake/system 与记忆不符 → 无语义命中
	hits, err = svc.SearchMemories(context.Background(), 7, "上海", 5)
	if err != nil {
		t.Fatalf("SearchMemories(7): %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("回落系统默认后异构向量不应语义命中, got %+v", hits)
	}
}

// fakeResolver 用户→provider 假解析器。
type fakeResolver struct {
	providers map[int64]EmbeddingProvider
}

func (f *fakeResolver) ResolveEmbeddingProvider(userID int64) EmbeddingProvider {
	return f.providers[userID] // 缺省 nil = 无用户级配置
}

func setResolver(svc MemoryService, r EmbeddingResolver) {
	if aware, ok := svc.(ResolverAware); ok {
		aware.SetEmbeddingResolver(r)
	}
}

// ===== 注入分层(PRD 修订:手动记忆常驻,自动记忆走工具,§6.5 修订) =====

// TestGetMemoryInjection_ManualOnly 注入只取手动记忆,自动记忆只出条数。
func TestGetMemoryInjection_ManualOnly(t *testing.T) {
	svc, db := retrievalSetup(t)
	manual := memorydomain.NewMemory(42, "用户偏好简洁回复")
	auto := memorydomain.NewAutoMemory(42, "用户是后端工程师", nil)
	if err := db.Create(manual).Error; err != nil {
		t.Fatalf("seed manual: %v", err)
	}
	if err := db.Create(auto).Error; err != nil {
		t.Fatalf("seed auto: %v", err)
	}

	manuals, autoCount, err := svc.GetMemoryInjection(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetMemoryInjection: %v", err)
	}
	if len(manuals) != 1 || manuals[0] != "用户偏好简洁回复" {
		t.Errorf("manual = %v, want 仅手动记忆", manuals)
	}
	if autoCount != 1 {
		t.Errorf("autoCount = %d, want 1", autoCount)
	}
}

// TestGetMemoryInjection_Empty 空库返回零值不报错。
func TestGetMemoryInjection_Empty(t *testing.T) {
	svc, _ := retrievalSetup(t)
	manuals, autoCount, err := svc.GetMemoryInjection(context.Background(), 42)
	if err != nil || len(manuals) != 0 || autoCount != 0 {
		t.Errorf("空库应返回零值, got %v/%d/%v", manuals, autoCount, err)
	}
}

// ===== M6.2 事项优先检索 =====

func retrievalSetupWithMatters(t *testing.T) (*gorm.DB, MemoryService) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Memory{}, &memorydomain.Matter{}))
	svc := NewMemoryService(
		memoryrepo.NewMemoryRepository(db),
		memoryrepo.NewMatterRepository(db),
		nil, nil,
	)
	return db, svc
}

// TestSearchMatters_TitleSubstringHit 标题子串命中(强信号)→ 返回事项+挂靠记忆全景;
// 状态子串单独命中(弱信号)不达阈值 → 不算命中。
func TestSearchMatters_TitleSubstringHit(t *testing.T) {
	db, svc := retrievalSetupWithMatters(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一西双版纳旅行", StateDesc: "机票别墅已订,交通未定",
		Status: memorydomain.MatterStatusActive,
	}))
	m, _ := matterRepo.GetByTitle(42, "十一西双版纳旅行")
	// 挂靠两条记忆
	require.NoError(t, db.Create(&memorydomain.Memory{
		UserID: 42, Content: "用户注重性价比", Source: memorydomain.MemorySourceAuto,
		Kind: memorydomain.MemoryKindFact, MatterID: &m.ID,
	}).Error)
	require.NoError(t, db.Create(&memorydomain.Memory{
		UserID: 42, Content: "待核实实时票价", Source: memorydomain.MemorySourceAuto,
		Kind: memorydomain.MemoryKindLoop, MatterID: &m.ID,
	}).Error)
	// 另一个事项,状态里才提到"旅行"(弱信号,不应命中)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "装修", StateDesc: "聊到旅行时顺便提了一句", Status: memorydomain.MatterStatusActive,
	}))

	hits, err := svc.SearchMatters(context.Background(), 42, "旅行", 5)
	require.NoError(t, err)
	require.Len(t, hits, 1, "只有标题命中的事项才算命中")
	require.Equal(t, "十一西双版纳旅行", hits[0].Matter.Title)
	require.Len(t, hits[0].Facts, 2, "应带出挂靠记忆全景")
}

// TestSearchMatters_Semantic 事项向量与查询同模型 → 余弦命中。
func TestSearchMatters_Semantic(t *testing.T) {
	db, svc := retrievalSetupWithMatters(t)
	emb := &fakeEmbedding{vectors: map[string][]float32{"旅行计划怎么样了": {1, 0, 0}}, name: "fake/m1"}
	if aware, ok := svc.(EmbeddingAware); ok {
		aware.SetEmbeddingProvider(emb)
	}
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一旅行", StateDesc: "推进中",
		Embedding: []float32{1, 0, 0}, EmbeddingModel: "fake/m1",
	}))

	hits, err := svc.SearchMatters(context.Background(), 42, "旅行计划怎么样了", 5)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	require.InDelta(t, 1.0, hits[0].Score, 0.001)
}
