package sandbox

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"omnibot/internal/domain/sandbox"
)

// ── DockerAPIClient mock ─────────────────────────────────────────────

type mockDockerAPI struct {
	mu sync.Mutex

	pingErr   error
	createErr error
	startErr  error
	stopErr   error
	removeErr error

	createCfg  *container.Config
	createHost *container.HostConfig
	onCreate   func(cfg *container.Config, host *container.HostConfig) // 断言 scratch 内容用

	waitCh    chan container.WaitResponse
	waitErrCh chan error

	logsReader io.ReadCloser

	started bool
	stopped bool
	removed int
}

func newMockDockerAPI() *mockDockerAPI {
	return &mockDockerAPI{
		waitCh:    make(chan container.WaitResponse, 1),
		waitErrCh: make(chan error, 1),
	}
}

func (m *mockDockerAPI) Ping(ctx context.Context) (types.Ping, error) {
	return types.Ping{}, m.pingErr
}

func (m *mockDockerAPI) ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.createErr != nil {
		return container.CreateResponse{}, m.createErr
	}
	m.createCfg, m.createHost = config, hostConfig
	if m.onCreate != nil {
		m.onCreate(config, hostConfig)
	}
	return container.CreateResponse{ID: "test-container"}, nil
}

func (m *mockDockerAPI) ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.started = true
	return m.startErr
}

func (m *mockDockerAPI) ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error) {
	return m.waitCh, m.waitErrCh
}

func (m *mockDockerAPI) ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error) {
	if m.logsReader == nil {
		return io.NopCloser(strings.NewReader("")), nil
	}
	return m.logsReader, nil
}

func (m *mockDockerAPI) ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = true
	return m.stopErr
}

func (m *mockDockerAPI) ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removed++
	return m.removeErr
}

// muxLogs 构造 docker 多路复用流(stdcopy 格式)。
func muxLogs(t *testing.T, stdout, stderr string) io.ReadCloser {
	t.Helper()
	var buf bytes.Buffer
	if stdout != "" {
		w := stdcopy.NewStdWriter(&buf, stdcopy.Stdout)
		if _, err := w.Write([]byte(stdout)); err != nil {
			t.Fatal(err)
		}
	}
	if stderr != "" {
		w := stdcopy.NewStdWriter(&buf, stdcopy.Stderr)
		if _, err := w.Write([]byte(stderr)); err != nil {
			t.Fatal(err)
		}
	}
	return io.NopCloser(bytes.NewReader(buf.Bytes()))
}

// T11 socket 解析:DOCKER_HOST 优先;默认 socket 不存在时回落 Docker Desktop 的
// ~/.docker/run/docker.sock(macOS context 布局,/var/run/docker.sock 常缺)。
func TestResolveDockerHost(t *testing.T) {
	if got, ok := resolveDockerHost("nobody-has-this-home"); ok {
		t.Errorf("无任何 socket 时不应命中回落, got %q", got)
	}
	fakeHome := t.TempDir()
	sockDir := filepath.Join(fakeHome, ".docker", "run")
	if err := os.MkdirAll(sockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sockDir, "docker.sock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveDockerHost(fakeHome)
	if !ok || got != "unix://"+filepath.Join(sockDir, "docker.sock") {
		t.Errorf("resolveDockerHost = %q,%v; want desktop socket", got, ok)
	}
}

// ── 用例 ─────────────────────────────────────────────────────────────

func okSpec() sandbox.SandboxSpec {
	return sandbox.SandboxSpec{
		Cmd:      []string{"python3", "main.py"},
		Timeout:  5 * time.Second,
		MemoryMB: 256,
		CPUCores: 0.5,
	}
}

func run(t *testing.T, api *mockDockerAPI, spec sandbox.SandboxSpec) (sandbox.SandboxResult, error) {
	t.Helper()
	e := NewDockerExecutor(api, DockerConfig{})
	return e.Run(context.Background(), spec)
}

// T1 容器创建参数硬化:断网/只读/四重限额/非 root/唯一挂载点/不透传宿主 env。
func TestRun_HardensContainerConfig(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}

	spec := okSpec()
	spec.Env = []string{"FOO=bar"}
	if _, err := run(t, mock, spec); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cfg, host := mock.createCfg, mock.createHost
	if cfg == nil || host == nil {
		t.Fatal("ContainerCreate 未被调用")
	}
	if host.NetworkMode != "none" {
		t.Errorf("NetworkMode = %q, want none(断网)", host.NetworkMode)
	}
	if !host.ReadonlyRootfs {
		t.Error("ReadonlyRootfs = false, want true")
	}
	if host.Resources.Memory != 256*1024*1024 {
		t.Errorf("Memory = %d, want %d", host.Resources.Memory, 256*1024*1024)
	}
	if host.Resources.NanoCPUs != int64(0.5*1e9) {
		t.Errorf("NanoCPUs = %d, want %d", host.Resources.NanoCPUs, int64(0.5*1e9))
	}
	if host.Resources.PidsLimit == nil || *host.Resources.PidsLimit != 64 {
		t.Errorf("PidsLimit = %v, want 64", host.Resources.PidsLimit)
	}
	if cfg.User != sandboxUser {
		t.Errorf("User = %q, want %q(非 root)", cfg.User, sandboxUser)
	}
	if cfg.WorkingDir != workspaceDir {
		t.Errorf("WorkingDir = %q, want %q", cfg.WorkingDir, workspaceDir)
	}
	if cfg.Image != "omnibot-sandbox:latest" {
		t.Errorf("Image = %q, want 默认镜像", cfg.Image)
	}
	if len(cfg.Cmd) != 2 || cfg.Cmd[0] != "python3" {
		t.Errorf("Cmd = %v, want argv 直传", cfg.Cmd)
	}
	if len(cfg.Env) != 1 || cfg.Env[0] != "FOO=bar" {
		t.Errorf("Env = %v, want 仅 Spec 显式声明项(不透传宿主环境)", cfg.Env)
	}
	if len(host.Mounts) != 1 || host.Mounts[0].Target != workspaceDir {
		t.Errorf("Mounts = %+v, want 唯一 /workspace 挂载", host.Mounts)
	}
}

// T2 输入文件写入 scratch 且挂载源指向它;非法路径被拒绝。
func TestRun_WritesFilesToScratch(t *testing.T) {
	var scratchSeen string
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}
	mock.onCreate = func(cfg *container.Config, host *container.HostConfig) {
		scratchSeen = host.Mounts[0].Source
		if got, err := os.ReadFile(filepath.Join(scratchSeen, "main.py")); err != nil || string(got) != "print(1)" {
			t.Errorf("scratch 中 main.py = %q, err=%v", got, err)
		}
	}

	spec := okSpec()
	spec.Files = []sandbox.File{{Name: "main.py", Content: "print(1)"}}
	if _, err := run(t, mock, spec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if scratchSeen == "" {
		t.Fatal("挂载源为空")
	}
	if _, err := os.Stat(scratchSeen); !os.IsNotExist(err) {
		t.Error("执行结束后 scratch 未清理")
	}
}

func TestRun_RejectsUnsafeFileNames(t *testing.T) {
	cases := []string{"../evil.py", "/etc/passwd", "a/../../b", "", ".."}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			mock := newMockDockerAPI()
			spec := okSpec()
			spec.Files = []sandbox.File{{Name: name, Content: "x"}}
			if _, err := run(t, mock, spec); err == nil {
				t.Errorf("Name=%q 应被拒绝", name)
			}
			if mock.createCfg != nil {
				t.Error("非法文件名不应触达 ContainerCreate")
			}
		})
	}
}

// T3 输出截断(独立于 stdout/stderr 各自限长)。
func TestRun_TruncatesOutput(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}
	mock.logsReader = muxLogs(t, strings.Repeat("a", 40*1024), strings.Repeat("b", 40*1024))

	res, err := run(t, mock, okSpec())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stdout) >= 40*1024 || !strings.Contains(res.Stdout, "已截断") {
		t.Errorf("stdout 未按限长截断(len=%d)", len(res.Stdout))
	}
	if len(res.Stderr) >= 40*1024 || !strings.Contains(res.Stderr, "已截断") {
		t.Errorf("stderr 未按限长截断(len=%d)", len(res.Stderr))
	}
}

// T4 正常回传:退出码 + stdout/stderr 解复用;非零退出码不是 error。
func TestRun_ExitCodeAndStreams(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 3}
	mock.logsReader = muxLogs(t, "hello\n", "oops\n")

	res, err := run(t, mock, okSpec())
	if err != nil {
		t.Fatalf("非零退出码不应返回 error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if res.Stdout != "hello\n" {
		t.Errorf("Stdout = %q", res.Stdout)
	}
	if res.Stderr != "oops\n" {
		t.Errorf("Stderr = %q", res.Stderr)
	}
}

// T5 超时:触发 ContainerStop 并返回可读超时错误。
func TestRun_TimeoutStopsContainer(t *testing.T) {
	mock := newMockDockerAPI() // wait 通道不发事件 → 模拟挂死
	spec := okSpec()
	spec.Timeout = 50 * time.Millisecond

	_, err := run(t, mock, spec)
	if err == nil {
		t.Fatal("超时应返回 error")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Errorf("错误信息应可读: %v", err)
	}
	if !mock.stopped {
		t.Error("超时未触发 ContainerStop")
	}
	if mock.removed == 0 {
		t.Error("超时后容器未清理")
	}
}

// T6 Docker 不可用 → 可引导错误(不隐藏工具,执行期引导)。
func TestRun_DockerUnavailableGuidance(t *testing.T) {
	mock := newMockDockerAPI()
	mock.pingErr = context.DeadlineExceeded

	_, err := run(t, mock, okSpec())
	if err == nil {
		t.Fatal("Docker 不可用应返回 error")
	}
	if !strings.Contains(err.Error(), "Docker") {
		t.Errorf("错误应引导用户检查 Docker: %v", err)
	}
	if mock.createCfg != nil {
		t.Error("不可用时不应触达 ContainerCreate")
	}
}

// T7 清理幂等:remove 失败不影响结果返回,remove 至少被调用。
func TestRun_CleanupIdempotent(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}
	mock.removeErr = os.ErrNotExist

	res, err := run(t, mock, okSpec())
	if err != nil {
		t.Fatalf("remove 失败不应影响结果: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
	if mock.removed == 0 {
		t.Error("ContainerRemove 未被调用")
	}
}

// T8 空 cmd 拒绝,不触达 Docker。
func TestRun_RejectsEmptyCmd(t *testing.T) {
	mock := newMockDockerAPI()
	spec := okSpec()
	spec.Cmd = nil
	if _, err := run(t, mock, spec); err == nil {
		t.Fatal("空 cmd 应被拒绝")
	}
	if mock.createCfg != nil {
		t.Error("空 cmd 不应触达 ContainerCreate")
	}
}

// T9 超时上限收敛:Spec 超限被 clamp,不报错。
func TestRun_ClampsTimeout(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}

	spec := okSpec()
	spec.Timeout = 10 * time.Hour // 远超默认上限 60s
	done := make(chan struct{})
	var res sandbox.SandboxResult
	go func() {
		res, _ = NewDockerExecutor(mock, DockerConfig{}).Run(context.Background(), spec)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("超限 Timeout 未被收敛,执行被拖住")
	}
	_ = res
}

// T10 断网兜底:即便 Spec.Network=true(未来放开前),实现层仍强制断网。
func TestRun_NetworkAlwaysDisabled(t *testing.T) {
	mock := newMockDockerAPI()
	mock.waitCh <- container.WaitResponse{StatusCode: 0}

	spec := okSpec()
	spec.Network = true
	if _, err := run(t, mock, spec); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if mock.createHost.NetworkMode != "none" {
		t.Errorf("NetworkMode = %q, want none(实现层强制断网)", mock.createHost.NetworkMode)
	}
}
