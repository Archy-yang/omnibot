package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"omnibot/internal/pkg/toolcore"
	larkauth "omnibot/internal/service/larkauth"
)

// feishu_profile 测试(19 号 M2):按 ctx 用户动态 --profile;未授权引导;无用户上下文回落现状。

// profileEchoStub 输出自身 argv(JSON 化由测试断言子串)。
const profileEchoStub = `printf '%s\n' "$@"`

type stubResolver struct {
	profile string
	err     error
	called  int64
}

func (s *stubResolver) ProfileForUser(userID int64) (string, error) {
	s.called = userID
	if s.err != nil {
		return "", s.err
	}
	return s.profile, nil
}

func runFeishuWithUser(t *testing.T, stub, argsJSON string, resolver *stubResolver, userID int64) (string, error) {
	t.Helper()
	bin := writeFeishuStub(t, stub)
	tool := CreateFeishuTool(FeishuCLIConfig{BinPath: bin, Resolver: resolver})
	args := map[string]interface{}{"args": []interface{}{"docs", "+search"}}
	_ = argsJSON
	ctx := context.Background()
	if userID > 0 {
		ctx = toolcore.WithUserID(ctx, userID)
	}
	return tool.Execute(ctx, args)
}

// T1 已授权用户 → argv 前插 --profile user-<id>。
func TestFeishuTool_ProfileInjected(t *testing.T) {
	resolver := &stubResolver{profile: "user-7"}
	out, err := runFeishuWithUser(t, profileEchoStub, "", resolver, 7)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(strings.TrimSpace(out), "--profile\nuser-7\ndocs"),
		"argv 应以 --profile user-7 开头: %q", out)
}

// T2 未授权 → 引导文案,不执行 CLI。
func TestFeishuTool_ProfileNotAuthorized(t *testing.T) {
	resolver := &stubResolver{err: larkauth.ErrNotAuthorized}
	out, err := runFeishuWithUser(t, profileEchoStub, "", resolver, 7)
	// 引导以非 error 文本返回(LLM 可读),或 error 均可——断言不执行 + 文案含"授权"
	if err != nil {
		assert.Contains(t, err.Error(), "授权")
	} else {
		assert.Contains(t, out, "授权")
	}
}

// T3 无用户上下文(userID=0)→ 不带 --profile,行为同现状。
func TestFeishuTool_NoUserContext(t *testing.T) {
	resolver := &stubResolver{profile: "user-7"}
	out, err := runFeishuWithUser(t, profileEchoStub, "", resolver, 0)
	require.NoError(t, err)
	assert.NotContains(t, out, "--profile")
	assert.Equal(t, int64(0), resolver.called, "无用户上下文不应查询 resolver")
}

// T4 resolver 未装配(nil)→ 行为同现状。
func TestFeishuTool_ResolverNil(t *testing.T) {
	bin := writeFeishuStub(t, profileEchoStub)
	tool := CreateFeishuTool(FeishuCLIConfig{BinPath: bin})
	out, err := tool.Execute(toolcore.WithUserID(context.Background(), 7),
		map[string]interface{}{"args": []interface{}{"docs", "+search"}})
	require.NoError(t, err)
	assert.NotContains(t, out, "--profile")
}

// ---- v2.13:工具撞到授权失效时自动发起重授权 ----

type stubAuthEnsurer struct {
	prompt  AuthPrompt
	err     error
	calls   int
	channel string
	target  string
}

func (s *stubAuthEnsurer) EnsureAuthPrompt(_ context.Context, _ int64, channel, target string) (AuthPrompt, error) {
	s.calls++
	s.channel, s.target = channel, target
	return s.prompt, s.err
}

// 未授权(DB 里就没有 authorized 行)→ 自动发起 device flow,把链接交给模型。
func TestFeishuTool_NotAuthorized_AutoStartsRelogin(t *testing.T) {
	bin := writeFeishuStub(t, profileEchoStub)
	ensurer := &stubAuthEnsurer{prompt: AuthPrompt{
		Status:    "pending",
		VerifyURL: "https://accounts.feishu.cn/oauth/v1/device/verify?user_code=ABCD-1234",
	}}
	tool := CreateFeishuTool(FeishuCLIConfig{
		BinPath:     bin,
		Resolver:    &stubResolver{err: larkauth.ErrNotAuthorized},
		AuthEnsurer: ensurer,
	})
	ctx := toolcore.WithUserID(context.Background(), 7)
	ctx = toolcore.WithSource(ctx, "feishu")
	ctx = toolcore.WithNotifyTarget(ctx, "ou_peer")

	_, err := tool.Execute(ctx, map[string]interface{}{"args": []interface{}{"docs", "+search"}})
	require.Error(t, err, "授权不可用仍应作为错误返回(模型据此回复用户)")
	assert.Equal(t, 1, ensurer.calls, "应自动发起一次重新授权")
	assert.Contains(t, err.Error(), "https://accounts.feishu.cn/oauth/v1/device/verify?user_code=ABCD-1234")
	assert.Contains(t, err.Error(), "自动完成")
	assert.Equal(t, "feishu", ensurer.channel, "发起渠道取自 ctx,收尾后才能推回同一会话")
	assert.Equal(t, "ou_peer", ensurer.target)
}

// CLI 报 token_missing(DB 还以为已授权)→ 同样自动发起,而不是把残缺 JSON 丢给模型。
func TestFeishuTool_CLITokenMissing_AutoStartsRelogin(t *testing.T) {
	// 假 CLI:输出与真实 lark-cli 同款的鉴权错误,并以非零码退出
	stub := `cat >&2 <<'JSON'
{"ok":false,"error":{"type":"authentication","subtype":"token_missing","message":"need_user_authorization"}}
JSON
exit 3`
	bin := writeFeishuStub(t, stub)
	ensurer := &stubAuthEnsurer{prompt: AuthPrompt{Status: "pending", VerifyURL: "https://verify/token-missing"}}
	tool := CreateFeishuTool(FeishuCLIConfig{
		BinPath:     bin,
		Resolver:    &stubResolver{profile: "user-7"},
		AuthEnsurer: ensurer,
	})

	_, err := tool.Execute(toolcore.WithUserID(context.Background(), 7),
		map[string]interface{}{"args": []interface{}{"docs", "+search"}})
	require.Error(t, err)
	assert.Equal(t, 1, ensurer.calls)
	assert.Contains(t, err.Error(), "https://verify/token-missing")
}

// 从未配置过应用 → 明确引导去 Web 首次配置。
func TestFeishuTool_NeedConfig(t *testing.T) {
	bin := writeFeishuStub(t, profileEchoStub)
	tool := CreateFeishuTool(FeishuCLIConfig{
		BinPath:     bin,
		Resolver:    &stubResolver{err: larkauth.ErrNotAuthorized},
		AuthEnsurer: &stubAuthEnsurer{prompt: AuthPrompt{Status: "none"}},
	})
	_, err := tool.Execute(toolcore.WithUserID(context.Background(), 7),
		map[string]interface{}{"args": []interface{}{"docs", "+search"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "AppID")
}
