package larkauth

// larkcli_real_test.go — 真实 CLI 适配层的解析与超时语义。
//
// 用桩脚本代替 lark-cli,验证的是"我们怎么读它的输出":
// 字段名漂移、available 缺省、JSON 结构变化都会在这里被抓住。
// 这几条曾经是盲区:包内其它用例都用 mockCLI,解析代码一次没真跑过。

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeCLIStub(t *testing.T, body string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fake-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\n"+body), 0o755))
	return bin
}

// LoginStart 解析 verification_url / device_code(用户码从链接 query 兜底)。
func TestCliReal_LoginStart_ParsesDeviceFlow(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
{"verification_url":"https://accounts.feishu.cn/oauth/v1/device/verify?flow_id=f1&user_code=ABCD-1234","device_code":"dev-1","expires_in":600}
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	url, userCode, deviceCode, err := cli.LoginStart(context.Background(), 1, "user-1", "")
	require.NoError(t, err)
	assert.Contains(t, url, "device/verify")
	assert.Equal(t, "ABCD-1234", userCode, "CLI 不单独给 user_code 时应从链接里取")
	assert.Equal(t, "dev-1", deviceCode)
}

// AuthStatus 把 CLI 的 identities.user 映射成我们的判定字段。
func TestCliReal_AuthStatus_MapsUserIdentity(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
{"appId":"cli_x","identities":{"bot":{"status":"ready","available":true},"user":{"status":"ok","available":true,"openId":"ou_1","userName":"杨琦"}}}
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	st, err := cli.AuthStatus(context.Background(), 1, "user-1")
	require.NoError(t, err)
	assert.True(t, st.UserAvailable)
	assert.Equal(t, "ok", st.UserStatus)
	assert.Equal(t, "ou_1", st.OpenID)
	assert.Equal(t, "杨琦", st.UserName)
}

// 某些版本省略 available,只给 status=ok —— 必须兼容,否则"明明能用却被判失效"。
func TestCliReal_AuthStatus_StatusOKImpliesAvailable(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
{"identities":{"user":{"status":"ok","openId":"ou_2","userName":"张三"}}}
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	st, err := cli.AuthStatus(context.Background(), 1, "user-1")
	require.NoError(t, err)
	assert.True(t, st.UserAvailable, "status=ok 应视为可用")
}

// token 缺失时 available=false、message 带 missing。
func TestCliReal_AuthStatus_MissingToken(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
{"identities":{"user":{"status":"missing","available":false,"openId":"ou_3"}}}
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	st, err := cli.AuthStatus(context.Background(), 1, "user-1")
	require.NoError(t, err)
	assert.False(t, st.UserAvailable)
	assert.Equal(t, "missing", st.UserStatus)
}

// whoami 的身份回显(onBehalfOf)。
func TestCliReal_Status_ParsesWhoami(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
{"onBehalfOf":{"openId":"ou_9","userName":"杨琦"}}
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	openID, name, err := cli.Status(context.Background(), 1, "user-1")
	require.NoError(t, err)
	assert.Equal(t, "ou_9", openID)
	assert.Equal(t, "杨琦", name)
}

// 输出不是 JSON(CLI 升级/报错混进 stdout)→ 明确报解析失败,不静默返回空身份。
func TestCliReal_AuthStatus_NonJSONErrors(t *testing.T) {
	bin := writeCLIStub(t, `echo "not json at all"`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	_, err := cli.AuthStatus(context.Background(), 1, "user-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解析失败")
}

// LoginComplete 必须把超时以 DeadlineExceeded 透出——上层据此判"用户还没确认"。
func TestCliReal_LoginComplete_PropagatesDeadline(t *testing.T) {
	bin := writeCLIStub(t, `sleep 5`)
	cli := NewRealCLI(CLIConfig{BinPath: bin, CompleteTimeout: 120 * time.Millisecond})

	start := time.Now()
	err := cli.LoginComplete(context.Background(), 1, "user-1", "dev-1")
	assert.Less(t, time.Since(start), 3*time.Second, "必须在配置的超时附近返回")
	assert.True(t, errors.Is(err, context.DeadlineExceeded), "超时要以 DeadlineExceeded 透出,实际: %v", err)
}

// Logout 真的调用了 auth logout(argv 直传)。
func TestCliReal_Logout_HitsCLI(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "args.txt")
	bin := writeCLIStub(t, `echo "$@" >> "`+marker+`"`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	require.NoError(t, cli.Logout(context.Background(), 1, "user-1"))
	raw, err := os.ReadFile(marker)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(raw), "auth logout"), "argv: %s", raw)
}

// profile 已存在、但 profile list 本身失败(CLI 抖动)→ 按"同应用"复用,不删任何东西。
func TestCliReal_ProfileAdd_ListFailureKeepsProfile(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "removed.txt")
	bin := writeCLIStub(t, `if [ "$1" = "profile" ]; then
  case "$2" in
    add) echo "profile already exists" >&2; exit 1 ;;
    list) echo "boom" >&2; exit 1 ;;
    remove) echo removed >> "`+marker+`" ;;
  esac
fi
exit 0
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	require.NoError(t, cli.ProfileAdd(context.Background(), 1, "user-1", "cli_new", "s"))
	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "读不到 profile 时不许做破坏性重建")
}

// 重建时 remove 失败 → 明确报错(不能假装重建成功继续跑)。
func TestCliReal_ProfileRebuild_RemoveFailure(t *testing.T) {
	bin := writeCLIStub(t, `if [ "$1" = "profile" ] && [ "$2" = "remove" ]; then
  echo "remove failed" >&2
  exit 1
fi
exit 0
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	err := cli.ProfileRebuild(context.Background(), 1, "user-1", "cli_x", "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remove")
}

// LoginStart 失败(CLI 报错)→ 错误透出,不返回空链接假装成功。
func TestCliReal_LoginStart_ErrorPropagates(t *testing.T) {
	bin := writeCLIStub(t, `echo "login failed: missing required parameter: client_secret" >&2; exit 3`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	_, _, _, err := cli.LoginStart(context.Background(), 1, "user-1", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "client_secret")
}

// 输出字段嵌在 data 子对象里(CLI 版本差异)→ 也要能解析。
func TestParseLoginStartOutput_NestedData(t *testing.T) {
	url, code, device, err := parseLoginStartOutput(`{"data":{"verification_url":"https://v?user_code=Z-9","device_code":"d-9"}}`)
	require.NoError(t, err)
	assert.Equal(t, "https://v?user_code=Z-9", url)
	assert.Equal(t, "Z-9", code)
	assert.Equal(t, "d-9", device)
}

// 注入了统一执行入口(27 号物化)时,授权链路必须走它,而不是直跑本地 CLI。
func TestCliReal_SetRunnerRoutesThroughExecutor(t *testing.T) {
	rr := &recordingRunner{}
	cli := NewRealCLI(CLIConfig{BinPath: "/nonexistent"})
	cli.(interface{ SetRunner(Runner) }).SetRunner(rr)

	_, _, _, err := cli.LoginStart(context.Background(), 7, "user-7", "")
	require.NoError(t, err)
	assert.Equal(t, int64(7), rr.userID, "必须带上用户维度(按用户物化 HOME)")
	assert.Contains(t, rr.args, "auth")
}

type recordingRunner struct {
	userID int64
	args   []string
}

func (r *recordingRunner) RunLarkCLI(_ context.Context, userID int64, _ string, args ...string) (string, error) {
	r.userID = userID
	r.args = args
	return `{"verification_url":"https://v?user_code=A-1","device_code":"d-1"}`, nil
}

// BinPath 为空 → 回落 "lark-cli"(与 Executor/feishu_tool 的约定一致)。
func TestCLIConfig_BinDefault(t *testing.T) {
	assert.Equal(t, "lark-cli", CLIConfig{}.bin())
	assert.Equal(t, "/custom/cli", CLIConfig{BinPath: "/custom/cli"}.bin())
}

// profile add 报的不是 "already"(真错误)→ 原样透出,不能当成"已存在"吞掉。
func TestCliReal_ProfileAdd_OtherErrorPropagates(t *testing.T) {
	bin := writeCLIStub(t, `echo "permission denied" >&2; exit 1`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	err := cli.ProfileAdd(context.Background(), 1, "user-1", "cli_x", "s")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "permission denied")
}

// whoami 失败 → 错误透出(上层据此判"还没好"),不能返回空身份当成功。
func TestCliReal_Status_ErrorPropagates(t *testing.T) {
	bin := writeCLIStub(t, `echo "not logged in" >&2; exit 1`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	_, _, err := cli.Status(context.Background(), 1, "user-1")
	require.Error(t, err)
}

// auth status 失败(CLI 挂了)→ 错误透出,由 Health 判为"环境问题,不改状态"。
func TestCliReal_AuthStatus_ErrorPropagates(t *testing.T) {
	bin := writeCLIStub(t, `echo "cli crashed" >&2; exit 2`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	_, err := cli.AuthStatus(context.Background(), 1, "user-1")
	require.Error(t, err)
}

// 两种"凭据缺失"文案都要认;无关错误不能误判。
func TestIsMissingSecretError(t *testing.T) {
	assert.True(t, isMissingSecretError(errors.New("missing required parameter: client_secret")))
	assert.True(t, isMissingSecretError(errors.New("app_secret not found")))
	assert.False(t, isMissingSecretError(errors.New("network unreachable")))
	assert.False(t, isMissingSecretError(nil))
}

// profile add 成功(新用户首次授权)→ 直接返回 nil。
func TestCliReal_ProfileAdd_Success(t *testing.T) {
	bin := writeCLIStub(t, `exit 0`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})
	require.NoError(t, cli.ProfileAdd(context.Background(), 1, "user-1", "cli_x", "s"))
}

// 错误尾部超过 400 字节时截断(避免把整段日志塞进错误信息)。
func TestCliReal_Run_TruncatesLongError(t *testing.T) {
	bin := writeCLIStub(t, `i=0; while [ $i -lt 40 ]; do echo "long-error-line-$i-xxxxxxxxxxxxxxxxxxxx" >&2; i=$((i+1)); done; exit 1`)
	real := NewRealCLI(CLIConfig{BinPath: bin}).(*cliReal)

	_, err := real.run(context.Background(), 1, "", "docs", "+search")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "long-error-line-39", "应保留尾部")
	assert.Less(t, len(err.Error()), 600, "错误信息要有上界")
}

// whoami 输出不是 JSON → 解析失败(不返回空身份)。
func TestCliReal_Status_NonJSON(t *testing.T) {
	bin := writeCLIStub(t, `echo "not json"`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})
	_, _, err := cli.Status(context.Background(), 1, "user-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "解析失败")
}

// auth login 输出不是 JSON → 解析失败。
func TestParseLoginStartOutput_NonJSON(t *testing.T) {
	_, _, _, err := parseLoginStartOutput("not json at all")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "期望 JSON")
}

// profile list 里没有目标 profile → 返回空 appId(调用方按"同应用"处理)。
func TestCliReal_ProfileAppID_NotFound(t *testing.T) {
	bin := writeCLIStub(t, `cat <<'JSON'
[{"name":"other","appId":"cli_other"}]
JSON
`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})
	appID, err := cli.(*cliReal).profileAppID(context.Background(), 1, "user-1")
	require.NoError(t, err)
	assert.Empty(t, appID)
}
