package larkhome

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testExecutor(t *testing.T) (*Executor, *Manager) {
	t.Helper()
	mgr, _ := setupManager(t)
	// 用一个假 CLI(打印 HOME 与参数)当 bin
	bin := filepath.Join(t.TempDir(), "fake-lark-cli")
	script := "#!/bin/sh\necho \"HOME=$HOME args=$*\"\nif [ -f \"$HOME/.lark-cli/config.json\" ]; then echo seen-config; fi\n"
	require.NoError(t, os.WriteFile(bin, []byte(script), 0o755))
	return NewExecutor(mgr, bin), mgr
}

func TestExecutor_Run_HomeInjected(t *testing.T) {
	exec, mgr := testExecutor(t)
	userID := int64(7)

	out, err := exec.RunLarkCLI(context.Background(), userID, "")
	require.NoError(t, err)
	assert.Contains(t, out, "HOME="+mgr.HomeDir(userID), "子进程应收到物化 HOME")
	assert.NotContains(t, out, "seen-config", "未授权用户目录应为空")

	// 授权后(blob 存在)→ 子进程能读到 config
	home := mgr.HomeDir(userID)
	require.NoError(t, os.MkdirAll(filepath.Join(home, relLarkCLI), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(home, relLarkCLI, "config.json"), []byte(`{"ok":1}`), 0o600))
	require.NoError(t, mgr.SyncBack(userID))
	require.NoError(t, os.RemoveAll(home))

	out, err = exec.RunLarkCLI(context.Background(), userID, "")
	require.NoError(t, err)
	assert.Contains(t, out, "seen-config", "hydrate 应让子进程看到授权状态")
}

func TestExecutor_Run_PanicInSyncBackNotFatal(t *testing.T) {
	exec, _ := testExecutor(t)
	out, err := exec.RunLarkCLI(context.Background(), 8, "")
	require.NoError(t, err)
	assert.NotEmpty(t, out)
}

func TestExecutor_PerUserSerialize(t *testing.T) {
	mgr, _ := setupManager(t)
	// bin 睡 100ms:同用户两次 Run 串行应 ≥200ms
	bin := filepath.Join(t.TempDir(), "slow-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nsleep 0.1\necho done\n"), 0o755))
	exec := NewExecutor(mgr, bin)

	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, _ = exec.RunLarkCLI(context.Background(), 9, "") }()
	}
	wg.Wait()
	assert.GreaterOrEqual(t, time.Since(start), 200*time.Millisecond, "同用户执行应串行")
}

func TestExecutor_RealBinaryArgvPassthrough(t *testing.T) {
	// argv 直传不经 shell 的既有纪律在 Executor 路径下保持:参数带空格/分号原样到达
	mgr, _ := setupManager(t)
	bin := filepath.Join(t.TempDir(), "echo-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' \"$1\" \"$2\"\n"), 0o755))
	exec := NewExecutor(mgr, bin)
	out, err := exec.RunLarkCLI(context.Background(), 1, "", "--weird arg", "a;b")
	require.NoError(t, err)
	assert.Equal(t, "--weird arg\na;b\n", out)
}

func TestExecutor_Stdin(t *testing.T) {
	mgr, _ := setupManager(t)
	bin := filepath.Join(t.TempDir(), "stdin-lark-cli")
	require.NoError(t, os.WriteFile(bin, []byte("#!/bin/sh\ncat\n"), 0o755))
	exec := NewExecutor(mgr, bin)
	out, err := exec.RunLarkCLI(context.Background(), 1, "secret-material", "profile", "add")
	require.NoError(t, err)
	assert.Equal(t, "secret-material", out)
}

// 回归(27 号上线首日):config 无 feishu.cli 段 → BinPath="" → 空命令静默失败。
// Executor 必须与既有三处 bin() 同款回落 "lark-cli"。
func TestExecutor_EmptyBinFallsBackToDefault(t *testing.T) {
	mgr, _ := setupManager(t)
	exec := NewExecutor(mgr, "")
	assert.Equal(t, "lark-cli", exec.bin, "空 BinPath 应回落 lark-cli")
}
