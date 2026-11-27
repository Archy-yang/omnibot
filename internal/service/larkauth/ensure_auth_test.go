package larkauth

// ensure_auth_test.go — v2.13 IM 内授权闭环:EnsureAuth 幂等矩阵。
//
// 核心契约:用户连着发几次 #飞书授权 不能把进行中的流程冲掉;
// 已授权直接返回;链接过期才换新的。

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userdomain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
)

func initCrypto(t *testing.T) {
	t.Helper()
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))
}

// encFor 造一个可解密的 secret 密文(授权行必须带密文才能被 EnsureAuth 复用)。
func encFor(t *testing.T, secret string) string {
	t.Helper()
	enc, err := crypto.Encrypt(secret)
	require.NoError(t, err)
	return enc
}

// TestEnsureAuth_AlreadyAuthorized 已授权且 CLI 有 token → 不开新流程。
func TestEnsureAuth_AlreadyAuthorized(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		startURL: "https://verify/1", startDevice: "dev-1",
		authStatusRes: AuthStatusResult{UserAvailable: true, UserStatus: "ok", OpenID: "ou_1"},
	}
	svc := newService(repo, cli)
	row := &userdomain.LarkCLIAuth{UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: encFor(t, "s")}
	row.Status = StatusAuthorized
	row.UserName = "杨琦"
	repo.rows[7] = row

	view, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_abc")
	require.NoError(t, err)
	assert.Equal(t, StatusAuthorized, view.Status)
	assert.Equal(t, 0, cli.startCalls, "已授权不得重新发起 device flow")
	assert.Equal(t, "feishu", repo.rows[7].NotifyChannel, "顺手记住发起渠道,便于日后失效时推回")
	assert.Equal(t, "ou_abc", repo.rows[7].NotifyTarget)
}

// TestEnsureAuth_AuthorizedButTokenGone → need_relogin 并发起新流程。
func TestEnsureAuth_AuthorizedButTokenGone(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		startURL: "https://verify/new", startUserCode: "CODE-1", startDevice: "dev-new",
		authStatusRes: AuthStatusResult{UserAvailable: false, UserStatus: "missing"},
	}
	svc := newService(repo, cli)
	row := &userdomain.LarkCLIAuth{UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: encFor(t, "s")}
	row.Status = StatusAuthorized
	repo.rows[7] = row

	view, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_abc")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status)
	assert.Equal(t, "https://verify/new", view.VerifyURL)
	assert.Equal(t, 1, cli.startCalls)
	assert.Equal(t, StatusPending, repo.rows[7].Status)
	assert.False(t, repo.rows[7].ExpiresAt.IsZero(), "必须记下 device code 到期时间")
}

// TestEnsureAuth_ReusesLivePending 未过期的 pending → 复用同一条链接,不重开。
func TestEnsureAuth_ReusesLivePending(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		startURL: "https://verify/first?flow_id=f1&user_code=CODE-1", startUserCode: "C1", startDevice: "dev-1",
	}
	svc := newService(repo, cli)
	// 先有一次「配置过应用」的历史记录(EnsureAuth 复用库里的 AppID/Secret)
	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "sec-1")
	require.NoError(t, err)
	require.Equal(t, 1, cli.startCalls)

	first, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err)
	require.Equal(t, 1, cli.startCalls, "已发起的流程不应被重开")

	// 用户又发了一次 #飞书授权(典型场景:等不及了连发)
	second, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err)

	assert.Equal(t, 1, cli.startCalls, "进行中的流程必须复用,不能重开(重开会把上次链接冲掉)")
	assert.Equal(t, first.VerifyURL, second.VerifyURL)
	assert.Equal(t, "dev-1", repo.rows[7].DeviceCode, "device code 不得被覆盖")
	assert.NotEmpty(t, second.UserCode, "复用时要能从链接里回显用户码")
	assert.Equal(t, "CODE-1", second.UserCode, "用户码从验证链接 query 回显")
}

// TestEnsureAuth_PendingExpired_StartsNew 过期 → 换新链接。
func TestEnsureAuth_PendingExpired_StartsNew(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		startURL: "https://verify/first?flow_id=f1&user_code=CODE-1", startUserCode: "C1", startDevice: "dev-1",
	}
	svc := newService(repo, cli)
	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "sec-1")
	require.NoError(t, err)
	_, err = svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err)

	cli.startURL, cli.startDevice = "https://verify/second", "dev-2"
	repo.rows[7].ExpiresAt = time.Now().Add(-time.Minute)

	view, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err)
	assert.Equal(t, 2, cli.startCalls, "过期后应换发新流程")
	assert.Equal(t, "https://verify/second", view.VerifyURL)
}

// TestEnsureAuth_NoRow 从未配置过应用 → none(必须去 Web 填 AppID/Secret)。
func TestEnsureAuth_NoRow(t *testing.T) {
	svc := NewService(newMockRepo(), &mockCLI{})
	view, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err)
	assert.Equal(t, "none", view.Status)
}

// TestEnsureAuth_ProbeErrorDoesNotStartFlow CLI 探测失败(环境问题)→ 报错,不贸然作废。
func TestEnsureAuth_ProbeErrorDoesNotStartFlow(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{authStatusErr: errors.New("lark-cli not found")}
	svc := newService(repo, cli)
	row := &userdomain.LarkCLIAuth{UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: encFor(t, "s")}
	row.Status = StatusAuthorized
	repo.rows[7] = row

	_, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.Error(t, err)
	assert.Equal(t, 0, cli.startCalls, "环境问题不该重开授权流程")
	assert.Equal(t, StatusAuthorized, repo.rows[7].Status, "也不该把用户标成需要重授权")
}
