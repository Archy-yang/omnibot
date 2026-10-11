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
