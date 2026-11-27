package larkauth

// profile_repair_test.go — v2.13 P2:profile 惰性修复。
//
// 契约:
//   - 同名同应用的已有 profile → 直接复用,绝不删除(删除会清空凭证)
//   - 只有 appId 变了、或 device flow 明确报 missing client_secret 时才重建
//   - 读不到 profile 信息时按"同应用"处理(宁可复用,不做破坏性操作)

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeProfileStub 造一个假 lark-cli:
//   - profile add → 首次报 already(模拟 profile 已存在),之后成功(模拟 remove 后重建)
//   - profile list → 输出 listJSON
//   - profile remove → 往 marker 文件写一行(用来断言"是否发生删除")
func writeProfileStub(t *testing.T, listJSON string) (bin, marker string) {
	t.Helper()
	dir := t.TempDir()
	marker = filepath.Join(dir, "removed.txt")
	added := filepath.Join(dir, "added.txt")
	bin = filepath.Join(dir, "fake-lark-cli")
	script := `#!/bin/sh
if [ "$1" = "profile" ]; then
  case "$2" in
    add)
      if [ -f "` + added + `" ]; then exit 0; fi
      : > "` + added + `"
      echo "profile already exists" >&2
      exit 1
      ;;
    list) cat <<'JSON'
` + listJSON + `
JSON
      ;;
    remove) echo "removed" >> "` + marker + `"; exit 0 ;;
  esac
fi
exit 0
`
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return bin, marker
}

// 同名同应用 → 复用,不删除。
func TestProfileAdd_SameApp_ReusesWithoutRemoving(t *testing.T) {
	bin, marker := writeProfileStub(t, `[{"name":"user-7","appId":"cli_same"}]`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	require.NoError(t, cli.ProfileAdd(context.Background(), 7, "user-7", "cli_same", "secret"))
	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "同应用必须复用,不得 remove(remove 会清空凭证)")
}

// appId 变了 → 必须重建,否则 device flow 会指向旧应用。
func TestProfileAdd_DifferentApp_Rebuilds(t *testing.T) {
	bin, marker := writeProfileStub(t, `[{"name":"user-7","appId":"cli_old"}]`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	require.NoError(t, cli.ProfileAdd(context.Background(), 7, "user-7", "cli_new", "secret"))
	_, statErr := os.Stat(marker)
	assert.NoError(t, statErr, "appId 变了必须重建 profile")
}

// profile list 输出不可解析 → 按"同应用"处理,不做破坏性重建。
func TestProfileAdd_UnparsableList_KeepsProfile(t *testing.T) {
	bin, marker := writeProfileStub(t, `not-a-json`)
	cli := NewRealCLI(CLIConfig{BinPath: bin})

	require.NoError(t, cli.ProfileAdd(context.Background(), 7, "user-7", "cli_new", "secret"))
	_, statErr := os.Stat(marker)
	assert.True(t, os.IsNotExist(statErr), "读不到信息时宁可复用,不擅自删除")
}

// ---- 惰性修复:device flow 报 missing client_secret 才重建并重试一次 ----

func TestStartAuth_RepairsMissingSecretOnce(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	cli := &mockCLI{
		startErrFirst: errors.New("lark-cli auth login 失败: missing required parameter: client_secret"),
		startURL:      "https://verify/ok", startUserCode: "C", startDevice: "dev-ok",
	}
	svc := NewService(repo, cli)

	view, err := svc.StartAuth(context.Background(), 7, "cli_x", "sec-1")
	require.NoError(t, err)
	assert.Equal(t, StatusPending, view.Status)
	assert.Equal(t, "https://verify/ok", view.VerifyURL)
	assert.Equal(t, []string{"user-7"}, cli.rebuilds, "缺 secret 时必须重建一次")
	assert.Equal(t, 2, cli.startCalls, "重建后应重试 device flow")
}

func TestStartAuth_RebuildFails(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	cli := &mockCLI{
		startErrFirst: errors.New("missing required parameter: client_secret"),
		rebuildErr:    errors.New("profile remove failed"),
	}
	svc := NewService(repo, cli)

	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "sec-1")
	require.Error(t, err)
	assert.Empty(t, repo.rows, "重建失败不应留下 pending 行")
}

// 其它登录错误(网络/参数)不触发重建——重建是破坏性操作,不能滥用。
func TestStartAuth_OtherError_NoRebuild(t *testing.T) {
	initCrypto(t)
	repo := newMockRepo()
	cli := &mockCLI{startErr: errors.New("network unreachable")}
	svc := NewService(repo, cli)

	_, err := svc.StartAuth(context.Background(), 7, "cli_x", "sec-1")
	require.Error(t, err)
	assert.Empty(t, cli.rebuilds, "非凭据损坏不该重建 profile")
}
