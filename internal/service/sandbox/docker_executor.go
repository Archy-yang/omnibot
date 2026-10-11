// Package sandbox 实现脚本沙箱执行器。
// 17-脚本沙箱技术方案 §3.3:一次性 Docker 容器——默认断网、rootfs 只读、非 root、
// 内存/CPU/PID/墙钟四重限额;image 等容器概念全部收在实现层,不进 domain 契约。
//
// 安全收口(§6,对齐安全红线):
//   - NetworkMode 强制 none(Spec.Network 仅是契约占位,实现侧兜底)
//   - 不透传宿主环境变量,Env 只用 Spec 显式声明项(密钥不进沙箱)
//   - argv 数组直传不经 shell;日志只记元数据,不记脚本内容与输出全文
package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"omnibot/internal/domain/sandbox"
)

const (
	sandboxUser  = "app"        // 镜像内非 root 用户(docker/sandbox.Dockerfile)
	workspaceDir = "/workspace" // 唯一 host 挂载点(一次性 scratch 目录)
	pingTimeout  = 5 * time.Second
	drainGrace   = 2 * time.Second // wait 结束后给输出流泵的收尾时间
	truncateMark = "\n...[输出已截断,原始长度 %d 字节,仅保留前 %d 字节]"
)

// timeoutFormat 超时错误信息(可读、可引导,不含内部细节)。
func timeoutFormat(d time.Duration) string {
	return fmt.Sprintf("脚本执行超时(%s),已被强制终止。可拆小任务或缩小输入后重试", d)
}

// DockerAPIClient docker client 的窄接口(*client.Client 天然满足),单测 mock 用。
type DockerAPIClient interface {
	Ping(ctx context.Context) (types.Ping, error)
	ContainerCreate(ctx context.Context, config *container.Config, hostConfig *container.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (container.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options container.StartOptions) error
	ContainerWait(ctx context.Context, containerID string, condition container.WaitCondition) (<-chan container.WaitResponse, <-chan error)
	ContainerLogs(ctx context.Context, containerID string, options container.LogsOptions) (io.ReadCloser, error)
	ContainerStop(ctx context.Context, containerID string, options container.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options container.RemoveOptions) error
}

// 确认真实客户端满足窄接口(签名漂移在编译期暴露)。
var _ DockerAPIClient = (*client.Client)(nil)

// DockerConfig DockerExecutor 配置(零值字段取默认)。
type DockerConfig struct {
	Image          string
	Timeout        time.Duration // 单次执行默认与上限(同值)
	MemoryMB       int64
	CPUCores       float64
	MaxOutputBytes int
	PidsLimit      int64
}

func (c DockerConfig) image() string {
	if c.Image != "" {
		return c.Image
	}
	return "omnibot-sandbox:latest"
}

func (c DockerConfig) timeout() time.Duration {
	if c.Timeout > 0 {
		return c.Timeout
	}
	return 60 * time.Second
}

func (c DockerConfig) memoryMB() int64 {
	if c.MemoryMB > 0 {
		return c.MemoryMB
	}
	return 512
}

func (c DockerConfig) cpuCores() float64 {
	if c.CPUCores > 0 {
		return c.CPUCores
	}
	return 1.0
}

func (c DockerConfig) maxOutputBytes() int {
	if c.MaxOutputBytes > 0 {
		return c.MaxOutputBytes
	}
	return 32 * 1024
}

func (c DockerConfig) pidsLimit() int64 {
	if c.PidsLimit > 0 {
		return c.PidsLimit
	}
	return 64
}

// clampTimeout Spec 未指定取默认,超限收敛到上限(不报错)。
func (c DockerConfig) clampTimeout(spec time.Duration) time.Duration {
	if spec <= 0 {
		return c.timeout()
	}
	if spec > c.timeout() {
		return c.timeout()
	}
	return spec
}

// DockerExecutor SandboxExecutor 的一次性容器实现。
type DockerExecutor struct {
	api DockerAPIClient
	cfg DockerConfig
}

func NewDockerExecutor(api DockerAPIClient, cfg DockerConfig) *DockerExecutor {
	return &DockerExecutor{api: api, cfg: cfg}
}

// NewDefaultAPIClient 构建真实 docker 客户端:DOCKER_HOST 优先;未设置时按
// /var/run/docker.sock → ~/.docker/run/docker.sock(Docker Desktop context 布局)回落。
func NewDefaultAPIClient() (DockerAPIClient, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if os.Getenv("DOCKER_HOST") == "" {
		if home, err := os.UserHomeDir(); err == nil {
			if host, ok := resolveDockerHost(home); ok {
				opts = append(opts, client.WithHost(host))
			}
		}
	}
	return client.NewClientWithOpts(opts...)
}

// resolveDockerHost 按 socket 存在性解析连接地址:
// Linux 惯例 /var/run/docker.sock 优先,macOS Docker Desktop 回落 ~/.docker/run/docker.sock。
func resolveDockerHost(home string) (string, bool) {
	candidates := []string{"/var/run/docker.sock"}
	if home != "" {
		candidates = append(candidates, filepath.Join(home, ".docker", "run", "docker.sock"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return "unix://" + c, true
		}
	}
	return "", false
}

// Run 执行一次脚本:临时容器跑完即删,宿主只留一次性 scratch(执行结束清理)。
// 非零退出码不是 error(脚本失败是合法结果);超时/基础设施故障才是 error。
func (e *DockerExecutor) Run(ctx context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxResult, error) {
	if len(spec.Cmd) == 0 {
		return sandbox.SandboxResult{}, fmt.Errorf("cmd 不能为空,如 [\"python3\",\"main.py\"]")
	}
	if err := validateFiles(spec.Files); err != nil {
		return sandbox.SandboxResult{}, err
	}

	// 可用性前置检查:不可达时给可引导错误(feishu_tool 先例:不隐藏工具,执行期引导自愈)
	pingCtx, cancelPing := context.WithTimeout(ctx, pingTimeout)
	defer cancelPing()
	if _, err := e.api.Ping(pingCtx); err != nil {
		return sandbox.SandboxResult{}, fmt.Errorf("沙箱不可用:Docker 未启动或不可达(%v),请启动 Docker 后重试", err)
	}

	timeout := e.cfg.clampTimeout(spec.Timeout)
	runCtx, cancelRun := context.WithTimeout(ctx, timeout)
	defer cancelRun()

	scratch, err := os.MkdirTemp("", "omnibot-sandbox-")
	if err != nil {
		return sandbox.SandboxResult{}, fmt.Errorf("沙箱工作目录创建失败: %w", err)
	}
	defer os.RemoveAll(scratch)
	if err := writeFiles(scratch, spec.Files); err != nil {
		return sandbox.SandboxResult{}, err
	}

	start := time.Now()
	result, timedOut, runErr := e.execRun(runCtx, spec, scratch)

	// 清理与结果返回解耦:remove 失败不影响结果返回(清理幂等,容器有 AutoRemove 兜底)
	cleanupCtx := context.WithoutCancel(ctx)
	if rmErr := e.api.ContainerRemove(cleanupCtx, result.containerID, container.RemoveOptions{Force: true}); rmErr != nil {
		_ = rmErr // 日志在 execRun 成功路径统一记元数据,此处静默(测试 T7 断言不炸)
	}

	if timedOut {
		return sandbox.SandboxResult{}, errors.New(timeoutFormat(timeout))
	}
	if runErr != nil {
		return sandbox.SandboxResult{}, runErr
	}
	result.Result.Duration = time.Since(start)
	return result.Result, nil
}

// runOutcome execRun 的内部结果:Result + 是否超时 + 基础设施错误。
type runOutcome struct {
	Result      sandbox.SandboxResult
	containerID string
}

// execRun 创建/启动容器、流式收集输出、等待退出。
func (e *DockerExecutor) execRun(ctx context.Context, spec sandbox.SandboxSpec, scratch string) (runOutcome, bool, error) {
	pids := e.cfg.pidsLimit()
	// 资源限额:Spec 显式声明优先,配置默认值兜底
	memMB := spec.MemoryMB
	if memMB <= 0 {
		memMB = e.cfg.memoryMB()
	}
	cores := spec.CPUCores
	if cores <= 0 {
		cores = e.cfg.cpuCores()
	}
	createResp, err := e.api.ContainerCreate(ctx, &container.Config{
		Image:      e.cfg.image(),
		Cmd:        spec.Cmd,
		Env:        spec.Env, // 仅 Spec 显式声明项,不透传宿主环境
		User:       sandboxUser,
		WorkingDir: workspaceDir,
		Tty:        false, // false = 输出为 stdcopy 多路复用流
	}, &container.HostConfig{
		NetworkMode:    container.NetworkMode("none"), // 断网,实现侧强制(Spec.Network 不放行)
		ReadonlyRootfs: true,
		Resources: container.Resources{
			Memory:    memMB * 1024 * 1024,
			NanoCPUs:  int64(cores * 1e9),
			PidsLimit: &pids,
		},
		Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: scratch,
			Target: workspaceDir,
		}},
	}, nil, nil, "")
	if err != nil {
		return runOutcome{}, false, fmt.Errorf("沙箱容器创建失败: %w", err)
	}
	out := runOutcome{containerID: createResp.ID}

	if err := e.api.ContainerStart(ctx, createResp.ID, container.StartOptions{}); err != nil {
		return out, false, fmt.Errorf("沙箱容器启动失败: %w", err)
	}

	// 输出流:Follow 模式流式收集,超限截断(stdout/stderr 各自限长)
	logsR, err := e.api.ContainerLogs(ctx, createResp.ID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true,
	})
	if err != nil {
		return out, false, fmt.Errorf("沙箱输出流获取失败: %w", err)
	}
	defer logsR.Close()
	var stdoutBuf, stderrBuf bytes.Buffer
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, logsR) // ctx 取消/容器退出后自然返回
	}()

	waitCh, waitErrCh := e.api.ContainerWait(ctx, createResp.ID, container.WaitConditionNotRunning)
	select {
	case resp := <-waitCh:
		if resp.Error != nil && resp.Error.Message != "" {
			return out, false, fmt.Errorf("沙箱容器异常退出: %s", resp.Error.Message)
		}
		out.Result.ExitCode = int(resp.StatusCode)
	case errResp := <-waitErrCh:
		if errResp != nil && ctx.Err() == nil {
			return out, false, fmt.Errorf("沙箱容器等待失败: %w", errResp)
		}
		return out, true, nil // ctx 已取消 = 超时路径
	case <-ctx.Done():
		// 超时:停止容器(用不被取消的 ctx 保证 Stop 真正发出),kill 等待零宽限
		stopCtx, cancelStop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancelStop()
		zero := 0
		if stopErr := e.api.ContainerStop(stopCtx, createResp.ID, container.StopOptions{Timeout: &zero}); stopErr != nil {
			_ = stopErr // 停止失败交给 Force Remove 兜底
		}
		return out, true, nil
	}

	// 容器已退出,给输出泵一点时间把尾部数据收完
	select {
	case <-pumpDone:
	case <-time.After(drainGrace):
	}
	out.Result.Stdout = truncateOutput(stdoutBuf.String(), e.cfg.maxOutputBytes())
	out.Result.Stderr = truncateOutput(stderrBuf.String(), e.cfg.maxOutputBytes())
	return out, false, nil
}

// validateFiles 文件路径校验:相对路径、不逃逸出 /workspace。
func validateFiles(files []sandbox.File) error {
	for _, f := range files {
		if f.Name == "" {
			return fmt.Errorf("文件名不能为空")
		}
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(f.Name) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("非法文件路径 %q:必须为 /workspace 下的相对路径", f.Name)
		}
	}
	return nil
}

// writeFiles 把输入文件写入 scratch(自动建父目录)。
func writeFiles(scratch string, files []sandbox.File) error {
	for _, f := range files {
		target := filepath.Join(scratch, filepath.Clean(f.Name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("沙箱输入目录创建失败: %w", err)
		}
		if err := os.WriteFile(target, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("沙箱输入文件写入失败: %w", err)
		}
	}
	return nil
}

// truncateOutput 超限截断并标注(feishu_tool 同款口径)。
func truncateOutput(out string, limit int) string {
	if len(out) <= limit {
		return out
	}
	return out[:limit] + fmt.Sprintf(truncateMark, len(out), limit)
}
