package feishudoc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 23 号 F1:个人文档同步——渲染纯函数 + 首建/幂等覆盖 + 错误语义。

func docSyncSetup(t *testing.T) (*Service, *gorm.DB, *stubRunner) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&memorydomain.Matter{}, &memorydomain.Memory{}))
	matterRepo := memoryrepo.NewMatterRepository(db)
	memoryRepo := memoryrepo.NewMemoryRepository(db)
	runner := &stubRunner{profile: "user-42", token: "doxcnABC", url: "https://xxx.feishu.cn/docx/doxcnABC"}
	svc := NewService(matterRepo, memoryRepo, runner, &stubProfiles{profile: "user-42"})
	return svc, db, runner
}

type stubProfiles struct{ profile string }

func (s *stubProfiles) ProfileForUser(int64) (string, error) { return s.profile, nil }

func (s *stubProfiles) fail() error { return errUnauthorized }

type stubRunner struct {
	profile    string
	title      string
	markdown   string
	token      string
	url        string
	createErr  error
	createCnt  int
	overwrote  int
	overTokens []string
}

func (s *stubRunner) SetRunner(Runner) {}

func (s *stubRunner) CreateDoc(_ context.Context, _ int64, profile, title, markdown string) (string, string, error) {
	s.createCnt++
	s.profile, s.title, s.markdown = profile, title, markdown
	if s.createErr != nil {
		return "", "", s.createErr
	}
	return s.token, s.url, nil
}

func (s *stubRunner) OverwriteDoc(_ context.Context, _ int64, profile, docToken, markdown string) error {
	s.overwrote++
	s.profile = profile
	s.overTokens = append(s.overTokens, docToken)
	s.markdown = markdown
	return nil
}

var errUnauthorized = errors.New("not authorized")

func TestRenderMatterMarkdown(t *testing.T) {
	ha := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m := MatterView{Title: "十一旅行", StateDesc: "机票已订", Status: "active"}
	items := []MemoryItemView{
		{Content: "置顶:预算上限 2 万", HappenedAt: &ha, Pinned: true},
		{Content: "订了酒店", HappenedAt: &ha},
		{Content: "早期想法", CreatedAt: ha}, // 无事件时间 → 用 created_at
	}
	md := RenderMatterMarkdown(m, items)

	assert.Contains(t, md, "# 十一旅行")
	assert.Contains(t, md, "由 OmniBot 自动维护", "必须含警示行")
	assert.Contains(t, md, "机票已订")
	assert.Contains(t, md, "- [2026-10-01] 置顶:预算上限 2 万 ★")
	assert.Contains(t, md, "- [2026-10-01] 订了酒店")
	assert.True(t, strings.Index(md, "置顶") < strings.Index(md, "订了酒店"))

	empty := RenderMatterMarkdown(MatterView{Title: "空档案"}, nil)
	assert.Contains(t, empty, "(暂无记录)")
}

// T2 首次同步 create + 回写;再次同步 overwrite 不重复建档。
func TestSyncMatter_CreateThenOverwrite(t *testing.T) {
	svc, db, runner := docSyncSetup(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "十一旅行", StateDesc: "机票已订", Status: memorydomain.MatterStatusActive,
	}))
	m, err := matterRepo.GetByTitle(42, "十一旅行")
	require.NoError(t, err)
	ha := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, db.Create(&memorydomain.Memory{
		UserID: 42, Content: "订了酒店", Kind: "fact", MatterID: &m.ID, HappenedAt: &ha,
	}).Error)

	res, err := svc.SyncMatter(context.Background(), 42, m.ID)
	require.NoError(t, err)
	assert.True(t, res.Created)
	assert.Equal(t, "https://xxx.feishu.cn/docx/doxcnABC", res.DocURL)
	assert.Equal(t, 1, runner.createCnt)
	assert.Equal(t, "user-42", runner.profile, "按用户 profile 执行")
	assert.Equal(t, "十一旅行", runner.title)
	assert.Contains(t, runner.markdown, "订了酒店")

	// 行已回写 token/url/synced_at
	updated, err := matterRepo.GetByID(42, m.ID)
	require.NoError(t, err)
	require.NotNil(t, updated)
	assert.Equal(t, "doxcnABC", updated.FeishuDocToken)
	assert.Equal(t, res.DocURL, updated.FeishuDocURL)
	assert.NotNil(t, updated.SyncedAt)

	// 再次同步:overwrite 同一文档,不 create
	res2, err := svc.SyncMatter(context.Background(), 42, m.ID)
	require.NoError(t, err)
	assert.False(t, res2.Created)
	assert.Equal(t, 1, runner.createCnt, "不重复建档")
	assert.Equal(t, 1, runner.overwrote)
	assert.Equal(t, []string{"doxcnABC"}, runner.overTokens)
}

// T3 错误语义:档案不存在/未授权飞书/CLI 失败。
func TestSyncMatter_Errors(t *testing.T) {
	svc, db, runner := docSyncSetup(t)
	matterRepo := memoryrepo.NewMatterRepository(db)
	require.NoError(t, matterRepo.UpsertByTitle(&memorydomain.Matter{
		UserID: 42, Title: "旅行", StateDesc: "x", Status: memorydomain.MatterStatusActive,
	}))
	m, _ := matterRepo.GetByTitle(42, "旅行")

	// 他人档案 → ErrMatterNotFound
	_, err := svc.SyncMatter(context.Background(), 99, m.ID)
	assert.ErrorIs(t, err, ErrMatterNotFound)

	// 未授权 → ErrNotAuthorized(在触碰 memoryRepo 之前短路)
	memoryRepo := memoryrepo.NewMemoryRepository(db)
	svcNoAuth := NewService(matterRepo, memoryRepo, runner, &failingProfiles{})
	_, err = svcNoAuth.SyncMatter(context.Background(), 42, m.ID)
	assert.ErrorIs(t, err, ErrNotAuthorized)

	// CLI 失败透传
	svcFail := NewService(matterRepo, memoryRepo, &stubRunner{createErr: errors.New("boom")}, &stubProfiles{profile: "user-42"})
	_, err = svcFail.SyncMatter(context.Background(), 42, m.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

type failingProfiles struct{}

func (failingProfiles) ProfileForUser(int64) (string, error) {
	return "", errUnauthorized
}
