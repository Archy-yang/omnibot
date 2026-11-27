package larkhome

// executor.go — 统一执行入口(27 号 §4):hydrate → HOME=<物化目录> exec → syncback。
// 三个调用点(feishu_tool / feishudoc / larkauth)全走这里,漏一处 = 该路径绕过物化、token 分叉。
// Runner 为 nil 时调用方回落直跑本地 CLI(单机 volume 形态的兼容路径)。

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// Runner 统一执行接口(调用点依赖此窄面,便于单测 stub)。
type Runner interface {
	// RunLarkCLI 以该用户身份执行 lark-cli。stdin 为空串时不接管道。
	RunLarkCLI(ctx context.Context, userID int64, stdin string, args ...string) (string, error)
}

// Executor Runner 的真实实现。
type Executor struct {
	mgr   *Manager
	bin   string
	locks sync.Map // userID → *sync.Mutex(per-user 串行;不同用户并行)
}

func NewExecutor(mgr *Manager, bin string) *Executor {
	if bin == "" {
		bin = "lark-cli" // 与 larkauth/feishudoc/feishu_tool 的 bin() 回落一致;空串会让 exec 静默失败
	}
	return &Executor{mgr: mgr, bin: bin}
}

// RunLarkCLI 执行一次 CLI 调用。执行失败也回写(部分路径会写日志/缓存,状态可能已变化)。
func (e *Executor) RunLarkCLI(ctx context.Context, userID int64, stdin string, args ...string) (string, error) {
	muAny, _ := e.locks.LoadOrStore(userID, &sync.Mutex{})
	mu := muAny.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	home, err := e.mgr.Hydrate(userID)
	if err != nil {
		return "", fmt.Errorf("larkhome: 物化用户 %d CLI 状态失败: %w", userID, err)
	}

	out, execErr := e.exec(ctx, home, stdin, args...)

	if sbErr := e.mgr.SyncBack(userID); sbErr != nil {
		logger.ErrorWithFields("larkhome: 执行后回写失败",
			zap.Int64("user_id", userID), zap.Error(sbErr))
	}
	return out, execErr
}

func (e *Executor) exec(ctx context.Context, homeDir, stdin string, args ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, e.bin, args...) //nolint:gosec // 受控 bin + argv 直传
	cmd.Env = append(cmd.Environ(), "HOME="+homeDir)
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("lark-cli 执行超时(60s)")
		}
		// 失败时要尽量保留 CLI 的完整错误:lark-cli 把可执行的修复建议(如
		// device flow 的 hint / 缺哪个 scope)写在错误 JSON 里,砍太狠模型就只能瞎猜。
		// 400 字节的旧实现曾把 "type":"authentication" 和 hint 前半段整段切掉。
		return "", fmt.Errorf("lark-cli 执行失败: %s", tailText(combineOutput(out.String(), errOut.String()), 2048))
	}
	return out.String(), nil
}

// combineOutput 合并 stdout/stderr(非空行才拼,避免开头多一个空行)。
func combineOutput(stdout, stderr string) string {
	stdout, stderr = strings.TrimSpace(stdout), strings.TrimSpace(stderr)
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

// tailText 超长时保留尾部(CLI 的错误与 hint 通常在后半段)。
func tailText(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return "..." + s[len(s)-limit:]
}
