package larkauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cliReal.run 被超时杀掉时必须保留 context.DeadlineExceeded 类型:
// 上层 PollAuth 据此区分"还没确认(保持 pending)"与"CLI 报错(failed)"。
// 曾经错误被吞成字符串,超时被误判为授权失败(19 号落地记录)。
func TestCliReal_Run_PreservesDeadlineExceeded(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "stub-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 5\n"), 0o755))

	cli := &cliReal{cfg: CLIConfig{BinPath: bin}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := cli.run(ctx, 1, "", "whoami")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "超时必须以 DeadlineExceeded 类型透出,而非字符串包装")
}

// WaitDelay:进程被杀后管道若被孙进程占用,Run 不得无限期滞留(实测曾把 5s 封顶拖到 10.7s)。
func TestCliReal_Run_WaitDelayBoundsReturn(t *testing.T) {
	// stub:父进程即 sleep,被杀后无孙进程;断言 5s ctx + WaitDelay 后 Run 在远小于 sleep 时长内返回
	dir := t.TempDir()
	bin := filepath.Join(dir, "stub-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 30\n"), 0o755))

	cli := &cliReal{cfg: CLIConfig{BinPath: bin}}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := cli.run(ctx, 1, "", "whoami")
	elapsed := time.Since(start)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Less(t, elapsed, 3*time.Second, "Run 应在超时后立即返回,不得滞留: %v", elapsed)
}

// errors.Is 组合:超时错误与普通 CLI 错误可区分(分类依据)。
func TestCliReal_Run_NonTimeoutErrorIsTyped(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "stub-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\necho boom >&2\nexit 7\n"), 0o755))

	cli := &cliReal{cfg: CLIConfig{BinPath: bin}}
	_, err := cli.run(context.Background(), 1, "", "whoami")
	require.Error(t, err)
	assert.False(t, errors.Is(err, context.DeadlineExceeded))
	assert.Contains(t, err.Error(), "boom")
}
