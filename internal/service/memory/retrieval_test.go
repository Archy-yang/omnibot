package memory

import (
	"context"
	"errors"
	"testing"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
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
	if err := db.AutoMigrate(&memorydomain.Memory{}, &memorydomain.ConversationDigest{}); err != nil {
		t.Fatalf("automigrate: %v", err)
	}
	repo := memoryrepo.NewMemoryRepository(db)
	digestRepo := memoryrepo.NewDigestRepository(db)
	return NewMemoryService(repo, digestRepo), db
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

// TestSearchDigests 纪要语义检索:命中且带溯源区间(TDD#11 服务侧)。
func TestSearchDigests(t *testing.T) {
	svc, db := retrievalSetup(t)
	setEmbedding(t, svc, &fakeEmbedding{
		vectors: map[string][]float32{
			"我们聊了什么": {1, 0, 0},
			"聊了租房":   {1, 0, 0},
			"聊了工作":   {0, 1, 0},
		},
		name: "fake/m1",
	})

	d1 := memorydomain.NewConversationDigest(42, "聊了租房", 1, 20, 20)
	d1.Embedding = []float32{1, 0, 0}
	d1.EmbeddingModel = "fake/m1"
	d2 := memorydomain.NewConversationDigest(42, "聊了工作", 21, 40, 20)
	d2.Embedding = []float32{0, 1, 0}
	d2.EmbeddingModel = "fake/m1"
	if err := db.Create(d1).Error; err != nil {
		t.Fatalf("seed d1: %v", err)
	}
	if err := db.Create(d2).Error; err != nil {
		t.Fatalf("seed d2: %v", err)
	}

	hits, err := svc.SearchDigests(context.Background(), 42, "我们聊了什么", 5)
	if err != nil {
		t.Fatalf("SearchDigests: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("got %d digest hits, want 1", len(hits))
	}
	if hits[0].Digest.Summary != "聊了租房" {
		t.Errorf("hit = %q, want 聊了租房", hits[0].Digest.Summary)
	}
	if hits[0].Digest.FromMessageID != 1 || hits[0].Digest.ToMessageID != 20 {
		t.Errorf("溯源区间 = [%d,%d], want [1,20]", hits[0].Digest.FromMessageID, hits[0].Digest.ToMessageID)
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
