package larkauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	userdomain "omnibot/internal/domain/user"
	"omnibot/internal/pkg/crypto"
)

// ---- mocks ----

type mockRepo struct {
	rows map[int64]*userdomain.LarkCLIAuth
}

func newMockRepo() *mockRepo { return &mockRepo{rows: map[int64]*userdomain.LarkCLIAuth{}} }

func (m *mockRepo) GetByUserID(userID int64) (*userdomain.LarkCLIAuth, error) {
	r, ok := m.rows[userID]
	if !ok {
		return nil, ErrNotFound
	}
	return r, nil
}

func (m *mockRepo) Upsert(row *userdomain.LarkCLIAuth) error { m.rows[row.UserID] = row; return nil }

func (m *mockRepo) Update(row *userdomain.LarkCLIAuth) error {
	if _, ok := m.rows[row.UserID]; !ok {
		return ErrNotFound
	}
	m.rows[row.UserID] = row
	return nil
}

func (m *mockRepo) Delete(userID int64) error { delete(m.rows, userID); return nil }

type mockCLI struct {
	profileAdds   [][2]string // {profile, appID}
	addErr        error
	startURL      string
	startUserCode string
	startDevice   string
	startErr      error
	completed     []string
	completeErr   error
	statusOpenID  string
	statusName    string
	statusErr     error
	logouts       []string
}

func (m *mockCLI) ProfileAdd(profile, appID, appSecret string) error {
	m.profileAdds = append(m.profileAdds, [2]string{profile, appID})
	return m.addErr
}

func (m *mockCLI) LoginStart(profile, domains string) (url, userCode, deviceCode string, err error) {
	return m.startURL, m.startUserCode, m.startDevice, m.startErr
}

func (m *mockCLI) LoginComplete(ctx context.Context, profile, deviceCode string) error {
	m.completed = append(m.completed, profile)
	return m.completeErr
}

func (m *mockCLI) Status(profile string) (openID, userName string, err error) {
	return m.statusOpenID, m.statusName, m.statusErr
}

func (m *mockCLI) Logout(profile string) error { m.logouts = append(m.logouts, profile); return nil }

func newService(repo *mockRepo, cli *mockCLI) *Service {
	return NewService(repo, cli)
}

// 视图状态断言简写(与 domain 常量同源)。
const (
	StatusPending    = userdomain.LarkAuthStatusPending
	StatusAuthorized = userdomain.LarkAuthStatusAuthorized
	StatusFailed     = userdomain.LarkAuthStatusFailed
)

func startInput() (int64, string, string) { return 7, "cli_app123", "sec-ret-1" }

// T1 StartAuth:pending 行落库(secret AES 加密)、profile add 调用、URL/device_code 回写;重复发起覆盖。
func TestStartAuth(t *testing.T) {
	// crypto 未初始化会导致 Encrypt 失败;测试包用固定密钥初始化
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))

	repo, cli := newMockRepo(), &mockCLI{
		startURL: "https://accounts.feishu.cn/verify?u=abc", startUserCode: "WXQR-1234", startDevice: "dev-1",
	}
	svc := newService(repo, cli)

	view, err := svc.StartAuth(context.Background(), 7, "cli_app123", "sec-ret-1")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status)
	assert.Equal(t, "https://accounts.feishu.cn/verify?u=abc", view.VerifyURL)
	assert.Equal(t, "WXQR-1234", view.UserCode)

	row := repo.rows[7]
	require.NotNil(t, row)
	assert.Equal(t, "user-7", row.ProfileName)
	assert.Equal(t, "cli_app123", row.AppID)
	assert.NotContains(t, row.AppSecretEnc, "sec-ret-1", "secret 必须密文落库")
	decrypted, err := crypto.Decrypt(row.AppSecretEnc)
	require.NoError(t, err)
	assert.Equal(t, "sec-ret-1", decrypted)
	assert.Equal(t, StatusPending, row.Status)
	assert.Equal(t, "dev-1", row.DeviceCode)
	assert.Equal(t, [][2]string{{"user-7", "cli_app123"}}, cli.profileAdds)

	// 重复发起:覆盖 pending(新 device code),不产生第二行
	cli2 := &mockCLI{startURL: "u2", startUserCode: "c2", startDevice: "dev-2"}
	svc2 := NewService(repo, cli2)
	view2, err := svc2.StartAuth(context.Background(), 7, "cli_app123", "sec-ret-2")
	require.NoError(t, err)
	assert.Equal(t, "u2", view2.VerifyURL)
	assert.Len(t, repo.rows, 1, "仍是一行")
	assert.Equal(t, "dev-2", repo.rows[7].DeviceCode)
}

// T2 PollAuth:确认成功→authorized+清 device_code;超时→保持 pending;CLI 报过期→failed。
func TestPollAuth(t *testing.T) {
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))

	// 成功路径
	repo, cli := newMockRepo(), &mockCLI{statusOpenID: "ou_x", statusName: "张三"}
	svc := newService(repo, cli)
	_, err := svc.StartAuth(context.Background(), 7, "cli_app123", "s")
	require.NoError(t, err)

	view, err := svc.PollAuth(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, StatusAuthorized, view.Status)
	assert.Equal(t, "ou_x", view.OpenID)
	assert.Equal(t, "张三", view.UserName)
	row := repo.rows[7]
	assert.Empty(t, row.DeviceCode, "收尾后清 device_code")
	assert.Empty(t, row.VerifiedURL)
	assert.Equal(t, []string{"user-7"}, cli.completed, "收尾须以本人 profile 执行")

	// 非 pending(已授权)→ 直接返回状态,不再触 CLI
	cli.completed = nil
	view, err = svc.PollAuth(context.Background(), 7)
	require.NoError(t, err)
	assert.Equal(t, StatusAuthorized, view.Status)
	assert.Empty(t, cli.completed)

	// 超时 → 保持 pending
	repo2, _ := newMockRepo(), &mockCLI{startURL: "u", startUserCode: "c", startDevice: "d"}
	cli2 := &mockCLI{completeErr: context.DeadlineExceeded}
	svc2 := NewService(repo2, cli2)
	_, err = svc2.StartAuth(context.Background(), 8, "a", "s")
	require.NoError(t, err)
	view, err = svc2.PollAuth(context.Background(), 8)
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status)
	assert.Equal(t, StatusPending, repo2.rows[8].Status)

	// CLI 报过期 → failed(可读)
	cli3 := &mockCLI{completeErr: errors.New("device code expired")}
	svc3 := NewService(repo2, cli3)
	view, err = svc3.PollAuth(context.Background(), 8)
	require.NoError(t, err)
	assert.Equal(t, StatusFailed, view.Status)
	assert.Equal(t, StatusFailed, repo2.rows[8].Status)
}

// T3 Disconnect:logout 调用 + 行删除;logout 失败不影响删除;无授权行 → ErrNotFound。
func TestDisconnect(t *testing.T) {
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))

	repo := newMockRepo()
	cli := &mockCLI{startURL: "u", startUserCode: "c", startDevice: "d"}
	logoutCLI := &logoutErrCLI{mockCLI: cli}
	svc := NewService(repo, logoutCLI)
	_, err := svc.StartAuth(context.Background(), 7, "a", "s")
	require.NoError(t, err)

	require.NoError(t, svc.Disconnect(context.Background(), 7))
	assert.Equal(t, []string{"user-7"}, logoutCLI.logouts)
	assert.NotContains(t, repo.rows, 7, "logout 失败也应删行(幂等自愈)")

	assert.ErrorIs(t, svc.Disconnect(context.Background(), 7), ErrNotFound)
}

type logoutErrCLI struct {
	*mockCLI
	logouts   []string
	logoutErr bool
}

func (m *logoutErrCLI) Logout(profile string) error {
	m.logouts = append(m.logouts, profile)
	if m.logoutErr {
		return errors.New("cli not found")
	}
	return nil
}

// T4 view 脱敏:任何 view 不含 secret 原文。
func TestViewNeverLeaksSecret(t *testing.T) {
	crypto.InitWithKey([]byte("01234567890123456789012345678901"))
	repo, cli := newMockRepo(), &mockCLI{startURL: "u", startUserCode: "c", startDevice: "d", statusName: "n", statusOpenID: "o"}
	svc := newService(repo, cli)
	v, err := svc.StartAuth(context.Background(), 9, "a", "super-secret-value")
	require.NoError(t, err)
	assert.False(t, strings.Contains(mustJSON(t, v), "super-secret-value"))
	_, err = svc.PollAuth(context.Background(), 9)
	require.NoError(t, err)
	authorized, err := svc.View(9)
	require.NoError(t, err)
	assert.False(t, strings.Contains(mustJSON(t, authorized), "super-secret-value"))
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
