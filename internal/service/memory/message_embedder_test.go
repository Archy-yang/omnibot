package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"omnibot/internal/domain/conversation"
	memorydomain "omnibot/internal/domain/memory"
)

// 消息嵌入器测试(M7 §10.5 / 测试清单#2#3):
// 独立水位推进/失败不推进;8 条分批;role 前缀+截断;无 provider 静默跳过;与 digest 水位互不影响。

// fakeEmbedSource 复用 digest 管线的消息区间接口。
type fakeEmbedSource struct {
	mu       sync.Mutex
	msgs     []*conversation.Message
	latestID int64
}

func (f *fakeEmbedSource) GetLatestMessageID(int64) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.latestID, nil
}

func (f *fakeEmbedSource) GetRangeByUserID(_ int64, afterID, toID int64) ([]*conversation.Message, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*conversation.Message
	for _, m := range f.msgs {
		if m.ID > afterID && m.ID <= toID {
			out = append(out, m)
		}
	}
	return out, nil
}

// fakeEmbProvider 记录每次调用的输入文本。
type fakeEmbProvider struct {
	mu     sync.Mutex
	calls  [][]string
	dim    int
	failAt int // 第 N 次 Embed 调用失败(1-based;0=不失败)
	calls0 int
}

func (f *fakeEmbProvider) Embed(_ context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls0++
	if f.failAt > 0 && f.calls0 == f.failAt {
		return nil, fmt.Errorf("embed boom")
	}
	cp := make([]string, len(texts))
	copy(cp, texts)
	f.calls = append(f.calls, cp)
	vs := make([][]float32, len(texts))
	for i := range vs {
		v := make([]float32, f.dim)
		v[0] = float32(f.calls0*100 + i)
		vs[i] = v
	}
	return vs, nil
}

func (f *fakeEmbProvider) Dim() int     { return f.dim }
func (f *fakeEmbProvider) Name() string { return "fake-emb" }

// fakeMsgEmbRepo 记录 upsert 批次与水位。
type fakeMsgEmbRepo struct {
	mu    sync.Mutex
	saved []*memorydomain.MessageEmbedding
	batch []int // 每次 UpsertBatch 的条数
}

func (f *fakeMsgEmbRepo) UpsertBatch(embs []*memorydomain.MessageEmbedding) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.batch = append(f.batch, len(embs))
	f.saved = append(f.saved, embs...)
	return nil
}

func (f *fakeMsgEmbRepo) ListByUserID(int64) ([]*memorydomain.MessageEmbedding, error) {
	return f.saved, nil
}

// fakeEmbWMRepo 内存水位。
type fakeEmbWMRepo struct {
	mu   sync.Mutex
	last map[int64]int64
}

func (f *fakeEmbWMRepo) GetByUserID(userID int64) (*memorydomain.EmbeddingWatermark, error) {
	return &memorydomain.EmbeddingWatermark{UserID: userID, LastEmbeddedMsgID: f.last[userID]}, nil
}

func (f *fakeEmbWMRepo) Save(userID int64, lastMsgID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.last == nil {
		f.last = make(map[int64]int64)
	}
	f.last[userID] = lastMsgID
	return nil
}

func msgOf(id int64, role, content string) *conversation.Message {
	return &conversation.Message{ID: id, UserID: 42, Role: role, Content: content}
}

func newEmbedder(src *fakeEmbedSource, emb EmbeddingProvider, repo *fakeMsgEmbRepo, wm *fakeEmbWMRepo) *MessageEmbedder {
	return NewMessageEmbedder(wm, repo, src, emb)
}

func TestMessageEmbedder_BackfillAndIncrement(t *testing.T) {
	src := &fakeEmbedSource{latestID: 3, msgs: []*conversation.Message{
		msgOf(1, "user", "第一条"),
		msgOf(2, "assistant", "第二条"),
		msgOf(3, "user", "第三条"),
	}}
	emb := &fakeEmbProvider{dim: 4}
	repo, wm := &fakeMsgEmbRepo{}, &fakeEmbWMRepo{}
	e := newEmbedder(src, emb, repo, wm)

	// 首轮:水位 0 → 存量全量回填
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(repo.saved) != 3 || wm.last[42] != 3 {
		t.Fatalf("回填应嵌 3 条且水位到 3, got %d 条水位 %d", len(repo.saved), wm.last[42])
	}
	if repo.saved[0].EmbeddingModel != "fake-emb" || repo.saved[0].Role != "user" {
		t.Errorf("落库字段不符: %+v", repo.saved[0])
	}

	// 增量:只嵌新消息
	src.msgs = append(src.msgs, msgOf(4, "user", "第四条"))
	src.latestID = 4
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if len(repo.saved) != 4 || wm.last[42] != 4 {
		t.Fatalf("增量应只嵌 1 条, got %d 条水位 %d", len(repo.saved), wm.last[42])
	}
}

func TestMessageEmbedder_RolePrefixAndTruncate(t *testing.T) {
	long := strings.Repeat("长", 2000)
	src := &fakeEmbedSource{latestID: 2, msgs: []*conversation.Message{
		msgOf(1, "assistant", "你好"),
		msgOf(2, "user", long),
	}}
	emb := &fakeEmbProvider{dim: 4}
	e := newEmbedder(src, emb, &fakeMsgEmbRepo{}, &fakeEmbWMRepo{})
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(emb.calls) != 1 || len(emb.calls[0]) != 2 {
		t.Fatalf("应一批嵌 2 条, calls=%v", emb.calls)
	}
	first := emb.calls[0][0]
	if !strings.HasPrefix(first, "[assistant] 你好") {
		t.Errorf("应带 role 前缀: %q", first)
	}
	if got := emb.calls[0][1]; utf8.RuneCountInString(got) != len("[user] ")+1500 {
		t.Errorf("超长应按 rune 截断为前缀+1500 字符, got %d", utf8.RuneCountInString(got))
	}
}

func TestMessageEmbedder_Batching(t *testing.T) {
	var msgs []*conversation.Message
	for i := int64(1); i <= 19; i++ {
		msgs = append(msgs, msgOf(i, "user", fmt.Sprintf("消息%d", i)))
	}
	src := &fakeEmbedSource{latestID: 19, msgs: msgs}
	emb := &fakeEmbProvider{dim: 4}
	repo := &fakeMsgEmbRepo{}
	e := newEmbedder(src, emb, repo, &fakeEmbWMRepo{})
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("run: %v", err)
	}
	// 19 条按 8/批 → 8+8+3
	if len(repo.batch) != 3 || repo.batch[0] != 8 || repo.batch[1] != 8 || repo.batch[2] != 3 {
		t.Fatalf("应 8/8/3 三批, got %v", repo.batch)
	}
}

func TestMessageEmbedder_FailureDoesNotAdvanceWatermark(t *testing.T) {
	src := &fakeEmbedSource{latestID: 10}
	for i := int64(1); i <= 10; i++ {
		src.msgs = append(src.msgs, msgOf(i, "user", fmt.Sprintf("m%d", i)))
	}
	emb := &fakeEmbProvider{dim: 4, failAt: 2} // 第二批失败
	repo, wm := &fakeMsgEmbRepo{}, &fakeEmbWMRepo{}
	e := newEmbedder(src, emb, repo, wm)

	if err := e.RunOnce(context.Background(), 42); err == nil {
		t.Fatal("第二批失败应返回错误")
	}
	// 第一批(8 条)已落库且水位推进到 8(逐批推进,重试只补失败区间)
	if wm.last[42] != 8 {
		t.Errorf("逐批推进:失败前批次水位应到 8, got %d", wm.last[42])
	}
	// 恢复后重试,只处理剩余 2 条
	emb.failAt = 0
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if len(repo.saved) != 10 || wm.last[42] != 10 {
		t.Errorf("重试应补完, got %d 条水位 %d", len(repo.saved), wm.last[42])
	}
}

func TestMessageEmbedder_NoProviderSkips(t *testing.T) {
	src := &fakeEmbedSource{latestID: 1, msgs: []*conversation.Message{msgOf(1, "user", "x")}}
	repo, wm := &fakeMsgEmbRepo{}, &fakeEmbWMRepo{}
	e := NewMessageEmbedder(wm, repo, src, nil)
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("无 provider 应静默跳过, got %v", err)
	}
	if len(repo.saved) != 0 || wm.last[42] != 0 {
		t.Errorf("无 provider 不应落库/推水位")
	}
}

func TestMessageEmbedder_IgnoresSystemMessages(t *testing.T) {
	src := &fakeEmbedSource{latestID: 2, msgs: []*conversation.Message{
		msgOf(1, "system", "系统注入"),
		msgOf(2, "user", "真话"),
	}}
	emb := &fakeEmbProvider{dim: 4}
	repo := &fakeMsgEmbRepo{}
	e := newEmbedder(src, emb, repo, &fakeEmbWMRepo{})
	if err := e.RunOnce(context.Background(), 42); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(repo.saved) != 1 || repo.saved[0].MessageID != 2 {
		t.Fatalf("只应嵌 user/assistant 消息, got %+v", repo.saved)
	}
}
