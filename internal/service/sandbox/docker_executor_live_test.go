package sandbox

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"omnibot/internal/domain/sandbox"
)

// Live smoke(17-脚本沙箱技术方案 测试计划 #4):真 Docker 全链路。
// 前置:LIVE_SMOKE=1 + Docker 运行中 + 沙箱镜像已构建(make sandbox-image)。
// 断言四条安全收口:正常执行回传、rootfs 只读、断网、超时击杀。

func liveExecutor(t *testing.T) *DockerExecutor {
	t.Helper()
	if os.Getenv("LIVE_SMOKE") != "1" {
		t.Skip("LIVE_SMOKE=1 时才运行真 Docker 冒烟")
	}
	api, err := NewDefaultAPIClient()
	if err != nil {
		t.Skipf("docker 客户端不可用: %v", err)
	}
	e := NewDockerExecutor(api, DockerConfig{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := api.Ping(ctx); err != nil {
		t.Skipf("docker daemon 不可达: %v", err)
	}
	return e
}

// L1 正常执行:python 算题结果经 stdout 回传。
func TestSandboxLive_PythonCompute(t *testing.T) {
	e := liveExecutor(t)
	res, err := e.Run(context.Background(), sandbox.SandboxSpec{
		Cmd:     []string{"python3", "main.py"},
		Files:   []sandbox.File{{Name: "main.py", Content: "import json\nprint(json.dumps({'sum': 1+2}))"}},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d, stderr: %s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, `"sum": 3`) && !strings.Contains(res.Stdout, `"sum":3`) {
		t.Errorf("Stdout = %q, want sum=3", res.Stdout)
	}
}

// L2 rootfs 只读:写系统路径被拒。
func TestSandboxLive_ReadOnlyRootfs(t *testing.T) {
	e := liveExecutor(t)
	res, err := e.Run(context.Background(), sandbox.SandboxSpec{
		Cmd:     []string{"python3", "-c", "open('/etc/omnibot-escape','w')"},
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("写 rootfs 竟然成功——只读失效")
	}
	if !strings.Contains(res.Stderr, "Read-only") && !strings.Contains(res.Stderr, "read-only") {
		t.Errorf("Stderr = %q, want 只读文件系统报错", res.Stderr)
	}
}

// L3 断网:socket 连接失败。
func TestSandboxLive_NetworkDisabled(t *testing.T) {
	e := liveExecutor(t)
	res, err := e.Run(context.Background(), sandbox.SandboxSpec{
		Cmd: []string{"python3", "-c", "import socket;s=socket.create_connection(('1.1.1.1',80),timeout=3)"},
		// 独立挂载 scratch 无网络可达性影响;timeout 15s > 内部 3s,让脚本自然失败
		Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("断网沙箱竟能建立外连——隔离失效")
	}
}

// L4 超时击杀:sleep 超限返回超时错误。
func TestSandboxLive_TimeoutKills(t *testing.T) {
	e := liveExecutor(t)
	start := time.Now()
	_, err := e.Run(context.Background(), sandbox.SandboxSpec{
		Cmd:     []string{"python3", "-c", "import time;time.sleep(60)"},
		Timeout: 3 * time.Second,
	})
	if err == nil {
		t.Fatal("超时应返回 error")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Errorf("错误应可读: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("超时未被及时击杀,耗时 %v", elapsed)
	}
}
