package larkauth

// service_edges_test.go — v2.13 授权服务的错误分支覆盖。
//
// 授权链路是"失败也要给出可执行出路"的场景,错误分支不是边角料:
// 解密失败 / 写库失败 / 查询失败 / whoami 抖动,每一条都决定用户下一步看到什么。

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userdomain "omnibot/internal/domain/user"
)

// 库里的密文坏了(密钥轮换/手工改库)→ 明确报错,不能静默发一个空 secret 给 CLI。
func TestEnsureAuth_DecryptError(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x",
		AppSecretEnc: "not-a-valid-ciphertext",
		Status:       StatusNeedRelogin,
	}
	svc := NewService(repo, &mockCLI{})

	_, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解密")
}

func TestRelogin_DecryptError(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x",
		AppSecretEnc: "broken", Status: StatusAuthorized,
	}
	svc := NewService(repo, &mockCLI{})

	_, err := svc.Relogin(context.Background(), 7)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解密")
}

// 缺 AppID/Secret 的调用属于编程错误,必须早失败。
func TestStartAuth_InvalidInput(t *testing.T) {
	initCrypto(t)
	svc := NewService(newMockRepo(), &mockCLI{})

	_, err := svc.StartAuth(context.Background(), 7, "", "secret")
	assert.ErrorIs(t, err, ErrInvalidInput)
	_, err = svc.StartAuth(context.Background(), 7, "cli_x", "")
	assert.ErrorIs(t, err, ErrInvalidInput)
}

// profile 写不进去(CLI 挂了/磁盘满)→ 直接失败,不能留下半截 pending 行。
func TestStartAuth_ProfileAddError(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	cli := &mockCLI{addErr: errors.New("profile add failed"), startURL: "u", startDevice: "d"}
	svc := NewService(repo, cli)

	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.Error(t, err)
	assert.Empty(t, repo.rows)
}

// 落库失败 → 把错误显式抛回(否则用户拿到链接但后台不知道这条 pending)。
func TestStartAuth_UpsertError(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	repo.upsertErr = errors.New("db write failed")
	cli := &mockCLI{startURL: "u", startDevice: "d"}
	svc := NewService(repo, cli)

	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.Error(t, err)
}

// ProfileForUser 是工具层的唯一入口:authorized 才给 profile 名。
func TestProfileForUser(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{startURL: "u", startDevice: "d"}
	svc := NewService(repo, cli)

	// 无行 → 未授权
	_, err := svc.ProfileForUser(7)
	assert.ErrorIs(t, err, ErrNotAuthorized)

	// pending → 仍未授权(等用户确认)
	_, err = svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.NoError(t, err)
	_, err = svc.ProfileForUser(7)
	assert.ErrorIs(t, err, ErrNotAuthorized, "pending 阶段不能把 profile 交给工具")

	// authorized → 返回 user-<id>
	repo.rows[7].Status = StatusAuthorized
	profile, err := svc.ProfileForUser(7)
	require.NoError(t, err)
	assert.Equal(t, "user-7", profile)
}

// 查询失败(DB 抖动)→ 视为未授权(宁可让用户重试,也不拿错 profile 去执行)。
func TestProfileForUser_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	_, err := svc.ProfileForUser(7)
	assert.ErrorIs(t, err, ErrNotAuthorized)
}

func TestView_NotFoundAndStoreError(t *testing.T) {
	svc := NewService(newMockRepo(), &mockCLI{})
	view, err := svc.View(7)
	require.NoError(t, err)
	assert.Equal(t, "none", view.Status)

	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc2 := NewService(repo, &mockCLI{})
	_, err = svc2.View(7)
	require.Error(t, err)
}

// 收尾正常结束但 whoami 抖动 → 保持 pending,下轮再补回显(不能判失败)。
func TestPollAuth_CompleteOKButWhoamiFails(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{startURL: "u", startDevice: "d"}
	svc := NewService(repo, cli)
	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.NoError(t, err)

	cli.completeErr = nil
	cli.statusErr = errors.New("whoami: temporarily unavailable")

	view, err := svc.PollAuth(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status, "whoami 抖动不等于授权失败")
	assert.NotEmpty(t, repo.rows[7].VerifiedURL, "验证链接必须留着,用户还能继续确认")
}

// 收尾成功但写库失败 → 返回错误,不假装成功。
func TestPollAuth_UpdateError(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{startURL: "u", startDevice: "d", statusOpenID: "ou_x"}
	svc := NewService(repo, cli)
	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.NoError(t, err)

	repo.updateErr = errors.New("db write failed")
	_, err = svc.PollAuth(context.Background(), 7)
	require.Error(t, err)
}

func TestPollAuth_NoRow(t *testing.T) {
	svc := NewService(newMockRepo(), &mockCLI{})
	view, err := svc.PollAuth(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, "none", view.Status)
}

// isTimeout 只对真正带 Timeout() 的错误返回 true。
func TestIsTimeout(t *testing.T) {
	assert.False(t, isTimeout(nil))
	assert.False(t, isTimeout(errors.New("plain error")))
	assert.True(t, isTimeout(timeoutErr{}))
}

type timeoutErr struct{}

func (timeoutErr) Error() string { return "i/o timeout" }
func (timeoutErr) Timeout() bool { return true }

// 已授权分支里顺手持久化 notify 绑定失败 → 不阻断(下次再记),仍返回可用。
func TestEnsureAuth_AuthorizedUpdateErrorIgnored(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: true, UserStatus: "ok"},
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x",
		AppSecretEnc: "unused", Status: StatusAuthorized,
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	view, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.NoError(t, err, "记不住 notify 绑定不该把已授权判成失败")
	assert.Equal(t, StatusAuthorized, view.Status)
}

func TestRelogin_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	_, err := svc.Relogin(context.Background(), 7)
	require.Error(t, err)
}

func TestDisconnect_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	require.Error(t, svc.Disconnect(context.Background(), 7))
}

// 收尾时读不到该行(被并发删除)→ 返回错误,由 sweeper 记日志跳过。
func TestPollAuth_StoreError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	_, err := svc.PollAuth(context.Background(), 7)
	require.Error(t, err)
}

// 查询授权行遇到非 not-found 错误 → 明确返回错误(不能当成"未配置")。
func TestEnsureAuth_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	_, err := svc.EnsureAuth(context.Background(), 7, "feishu", "ou_a")
	require.Error(t, err)
}

// channel/target 传空时继承上一次的 notify 绑定:
// 否则换了入口发起授权,收尾结果就不知道推回哪里。
func TestStartAuth_InheritsNotifyBinding(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{startURL: "u1", startDevice: "d1"}
	svc := NewService(repo, cli)

	// 第一次:IM 发起(带渠道与目标)
	_, err := svc.startAuth(context.Background(), 7, "cli_x", "s", "feishu", "ou_peer", 0)
	require.NoError(t, err)

	// 第二次:入口没带渠道信息 → 应继承
	cli.startURL, cli.startDevice = "u2", "d2"
	_, err = svc.startAuth(context.Background(), 7, "cli_x", "s", "", "", 0)
	require.NoError(t, err)

	assert.Equal(t, "feishu", repo.rows[7].NotifyChannel)
	assert.Equal(t, "ou_peer", repo.rows[7].NotifyTarget)
}

// 健康检查写库失败 → 返回错误(调用方据此提示"稍后重试",而不是假装已校正)。
func TestHealth_UpdateErrorPropagates(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: false, UserStatus: "missing"},
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x",
		AppSecretEnc: "x", Status: StatusAuthorized,
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}

// logout 失败也要删行(幂等自愈),不能因为 CLI 报错把记录留着。
func TestDisconnect_LogoutErrorStillDeletes(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{startURL: "u", startDevice: "d"}
	failing := &logoutErrCLI{mockCLI: cli, logoutErr: true}
	svc := NewService(repo, failing)
	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "s")
	require.NoError(t, err)

	require.NoError(t, svc.Disconnect(context.Background(), 7))
	assert.NotContains(t, repo.rows, int64(7))
}

func TestHealth_RepoError(t *testing.T) {
	repo := newMockRepo()
	repo.getErr = errors.New("db down")
	svc := NewService(repo, &mockCLI{})
	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}

// CLI 有 token 且身份变了 → 要回写;回写失败必须报错(不能假装校正成功)。
func TestHealth_AuthorizedUpdateError(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: true, UserStatus: "ok", UserName: "新名字"},
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: "x",
		Status: StatusAuthorized, UserName: "旧名字",
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}

// CLI 侧挂着 device flow 且收尾成功 → 回写失败也要报错。
func TestHealth_DeviceFlowUpdateError(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: false, UserStatus: "pending", DeviceCode: "dev-x"},
		statusOpenID:  "ou_done", statusName: "杨琦",
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: "x",
		Status: StatusAuthorized,
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}

// 明确无 token → 标 need_relogin;回写失败要报错。
func TestHealth_NeedReloginUpdateError(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: false, UserStatus: "missing"},
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: "x",
		Status: StatusAuthorized,
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}

// pending 期间健康检查直接返回现状,不抢 sweeper 的活(不打 CLI)。
func TestHealth_PendingReturnsWithoutProbing(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: "x",
		Status: StatusPending, DeviceCode: "dev-1", VerifiedURL: "https://v?user_code=A-1",
	}
	svc := NewService(repo, cli)

	view, err := svc.Health(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status)
	assert.Equal(t, 0, cli.authStatusCalls, "pending 由 sweeper 收尾,健康检查不该再探一次")
	assert.NotEmpty(t, view.VerifyURL)
}

// CLI 挂着 device flow 但收尾失败 → 标 need_relogin;写库失败要报错。
func TestHealth_DeviceFlowCompleteFailsUpdateError(t *testing.T) {
	initCrypto(t)
	repo, cli := newMockRepo(), &mockCLI{
		authStatusRes: AuthStatusResult{UserAvailable: false, UserStatus: "pending", DeviceCode: "dev-x"},
		completeErr:   errors.New("device code expired"),
	}
	repo.rows[7] = &userdomain.LarkCLIAuth{
		UserID: 7, ProfileName: "user-7", AppID: "cli_x", AppSecretEnc: "x",
		Status: StatusAuthorized,
	}
	repo.updateErr = errors.New("db write failed")
	svc := NewService(repo, cli)

	_, err := svc.Health(context.Background(), 7)
	require.Error(t, err)
}
