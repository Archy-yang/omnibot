// Package feishudoc 实现个人文档同步(23-Document Integration 技术方案):
// 把 Matter 档案(21 号)一键同步为用户名下的飞书文档——首次 create,再次 overwrite 幂等更新。
// 执行纪律同 feishu_tool(13 号 §7.5):argv 直传不经 shell、超时、stdin 传正文、输出截断。
package feishudoc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	memorydomain "omnibot/internal/domain/memory"
	memoryrepo "omnibot/internal/repository/memory"
)

var (
	ErrMatterNotFound = errors.New("feishudoc: 档案不存在")
	ErrNotAuthorized  = errors.New("feishudoc: 尚未完成飞书授权")
	ErrDocOutput      = errors.New("feishudoc: 文档输出缺少关键字段")
)

// LarkDocRunner lark-cli 文档能力窄接口(单测 stub)。
type LarkDocRunner interface {
	// CreateDoc 在用户空间(my_library)创建 markdown 文档,返回 (token, url)。
	// userID:27 号 DB 物化后执行需按用户 hydrate/回写。
	CreateDoc(ctx context.Context, userID int64, profile, title, markdown string) (string, string, error)
	// OverwriteDoc 全量覆盖既有文档内容。
	OverwriteDoc(ctx context.Context, userID int64, profile, docToken, markdown string) error

	// SetRunner 注入统一执行入口(27 号;nil = 直跑本地 CLI)。
	SetRunner(r Runner)
}

// Runner 统一执行入口(27 号 larkhome.Executor;nil = 直跑本地 CLI)。
type Runner interface {
	RunLarkCLI(ctx context.Context, userID int64, stdin string, args ...string) (string, error)
}

// ProfileSource 按用户解析 lark-cli profile(19 号;实现=larkauth.Service)。
type ProfileSource interface {
	ProfileForUser(userID int64) (string, error)
}

// SyncResult 同步结果。
type SyncResult struct {
	DocURL   string    `json:"doc_url"`
	SyncedAt time.Time `json:"synced_at"`
	Created  bool      `json:"created"`
}

// MatterView 渲染输入(与 memorydomain 解耦,便于单测)。
type MatterView struct {
	Title     string
	StateDesc string
	Status    string
}

// MemoryItemView 时间线条目。
type MemoryItemView struct {
	Content    string
	Kind       string
	Pinned     bool
	HappenedAt *time.Time
	CreatedAt  time.Time
}

// Service 档案→飞书文档同步编排。
type Service struct {
	matterRepo memoryrepo.MatterRepository
	memoryRepo memoryrepo.MemoryRepository
	runner     LarkDocRunner
	profiles   ProfileSource
}

func NewService(matterRepo memoryrepo.MatterRepository, memoryRepo memoryrepo.MemoryRepository, runner LarkDocRunner, profiles ProfileSource) *Service {
	return &Service{matterRepo: matterRepo, memoryRepo: memoryRepo, runner: runner, profiles: profiles}
}

const docDisclaimer = "> 本文档由 OmniBot 自动维护,手动编辑会被下次同步覆盖;" +
	"如需修正内容,请在 OmniBot 中纠正记忆后重新同步。"

var statusLabel = map[string]string{
	memorydomain.MatterStatusActive:   "进行中",
	memorydomain.MatterStatusDone:     "已完成",
	memorydomain.MatterStatusArchived: "已归档",
}

// RenderMatterMarkdown 纯函数渲染(单测不需要 CLI)。
func RenderMatterMarkdown(m MatterView, items []MemoryItemView) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", m.Title)
	b.WriteString(docDisclaimer + "\n\n")
	fmt.Fprintf(&b, "**当前状态**:%s\n\n", orDash(m.StateDesc))
	if label, ok := statusLabel[m.Status]; ok {
		fmt.Fprintf(&b, "**档案状态**:%s\n\n", label)
	}
	b.WriteString("## 时间线\n\n")
	if len(items) == 0 {
		b.WriteString("(暂无记录)\n")
		return b.String()
	}
	for _, it := range items {
		date := it.CreatedAt
		if it.HappenedAt != nil {
			date = *it.HappenedAt
		}
		pin := ""
		if it.Pinned {
			pin = " ★"
		}
		fmt.Fprintf(&b, "- [%s] %s%s\n", date.Format("2006-01-02"), it.Content, pin)
	}
	return b.String()
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(暂无)"
	}
	return s
}

// SyncMatter 同步档案:归属校验 → profile 解析 → 渲染 → 首建/幂等覆盖 → 回写。
func (s *Service) SyncMatter(ctx context.Context, userID, matterID int64) (SyncResult, error) {
	m, err := s.matterRepo.GetByID(userID, matterID)
	if err != nil {
		return SyncResult{}, err
	}
	if m == nil {
		return SyncResult{}, ErrMatterNotFound
	}
	profile, err := s.profiles.ProfileForUser(userID)
	if err != nil {
		return SyncResult{}, ErrNotAuthorized
	}
	items, err := s.memoryRepo.ListByMatter(userID, matterID)
	if err != nil {
		return SyncResult{}, err
	}
	views := make([]MemoryItemView, 0, len(items))
	for _, it := range items {
		views = append(views, MemoryItemView{
			Content: it.Content, Kind: it.Kind, Pinned: it.Pinned,
			HappenedAt: it.HappenedAt, CreatedAt: it.CreatedAt,
		})
	}
	markdown := RenderMatterMarkdown(MatterView{Title: m.Title, StateDesc: m.StateDesc, Status: m.Status}, views)

	now := time.Now()
	created := false
	docURL := m.FeishuDocURL
	if m.FeishuDocToken == "" {
		token, url, err := s.runner.CreateDoc(ctx, userID, profile, m.Title, markdown)
		if err != nil {
			return SyncResult{}, err
		}
		created = true
		docURL = url
		ok, err := s.matterRepo.UpdateDocInfo(userID, m.ID, token, url, now)
		if err != nil || !ok {
			return SyncResult{}, fmt.Errorf("feishudoc: 文档信息回写失败: %w", err)
		}
	} else {
		if err := s.runner.OverwriteDoc(ctx, userID, profile, m.FeishuDocToken, markdown); err != nil {
			return SyncResult{}, err
		}
		ok, err := s.matterRepo.UpdateDocInfo(userID, m.ID, m.FeishuDocToken, m.FeishuDocURL, now)
		if err != nil || !ok {
			return SyncResult{}, fmt.Errorf("feishudoc: 同步时间回写失败: %w", err)
		}
	}
	return SyncResult{DocURL: docURL, SyncedAt: now, Created: created}, nil
}

// ---- 真实 CLI 实现 ----

// CLIConfig 真实 runner 配置。
type CLIConfig struct {
	BinPath string        // 默认 lark-cli
	Timeout time.Duration // 默认 60s
}

func (c CLIConfig) bin() string {
	if c.BinPath != "" {
		return c.BinPath
	}
	return "lark-cli"
}

func (c CLIConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 60 * time.Second
}

// NewRealRunner 构建真实 CLI runner。
func NewRealRunner(cfg CLIConfig) LarkDocRunner { return &cliRunner{cfg: cfg} }

type cliRunner struct{ cfg CLIConfig; runner Runner }

// run 执行 lark-cli(argv 直传,stdin 传正文),返回 stdout。
func (r *cliRunner) SetRunner(runner Runner) { r.runner = runner }

func (r *cliRunner) run(ctx context.Context, userID int64, stdin, profile string, args ...string) (string, error) {
	if r.runner != nil {
		// --profile 与直跑路径一致(物化 HOME 可能含多个 profile,缺省会落错身份)
		return r.runner.RunLarkCLI(ctx, userID, stdin, append([]string{"--profile", profile}, args...)...)
	}
	runCtx, cancel := context.WithTimeout(ctx, r.cfg.timeout())
	defer cancel()
	full := append([]string{"--profile", profile}, args...)
	cmd := exec.CommandContext(runCtx, r.cfg.bin(), full...) //nolint:gosec // 受控 bin + argv 直传
	cmd.WaitDelay = time.Second
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", errors.New("feishudoc: lark-cli 执行超时")
		}
		tail := errOut.String()
		if len(tail) > 400 {
			tail = tail[len(tail)-400:]
		}
		return "", fmt.Errorf("feishudoc: lark-cli 执行失败: %s", strings.TrimSpace(tail))
	}
	return out.String(), nil
}

// pickString 多候选字段宽容解析(19 号同款纪律)。
func pickString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok && v != "" {
			return v
		}
	}
	return ""
}

func parseDocOutput(out string) (token, url string, err error) {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return "", "", fmt.Errorf("feishudoc: 输出解析失败(期望 JSON): %w", err)
	}
	if data, ok := m["data"].(map[string]interface{}); ok {
		for k, v := range data {
			if _, exists := m[k]; !exists {
				m[k] = v
			}
		}
	}
	token = pickString(m, "document_id", "doc_token", "token", "docToken")
	url = pickString(m, "url", "doc_url", "document_url")
	if token == "" {
		return "", "", ErrDocOutput
	}
	return token, url, nil
}

func (r *cliRunner) CreateDoc(ctx context.Context, userID int64, profile, title, markdown string) (string, string, error) {
	out, err := r.run(ctx, userID, markdown, profile,
		"docs", "+create", "--title", title, "--doc-format", "markdown",
		"--content", "-", "--parent-position", "my_library")
	if err != nil {
		return "", "", err
	}
	return parseDocOutput(out)
}

func (r *cliRunner) OverwriteDoc(ctx context.Context, userID int64, profile, docToken, markdown string) error {
	_, err := r.run(ctx, userID, markdown, profile,
		"docs", "+update", "--doc", docToken, "--command", "overwrite",
		"--doc-format", "markdown", "--content", "-")
	return err
}
