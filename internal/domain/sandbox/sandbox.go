// Package sandbox 定义沙箱执行的中立契约(接口 + DTO)。
// 17-脚本沙箱技术方案 §3.2:抽象层不含任何容器概念(image 等实现细节归实现层),
// 上游(工具层)只依赖 SandboxExecutor,不感知背后是 Docker 还是其他隔离实现。
package sandbox

import (
	"context"
	"time"
)

// File 注入沙箱的输入文件(脚本/数据)。Name 为相对 /workspace 的路径。
type File struct {
	Name    string
	Content string
}

// SandboxSpec 描述"要跑什么",不描述"在什么上跑"。
type SandboxSpec struct {
	Cmd      []string      // argv 直传不经 shell(防注入,feishu_tool 同款纪律)
	Stdin    string        // 多行内容走 stdin
	Files    []File        // 输入文件
	Env      []string      // K=V,仅显式声明项;不透传宿主环境(密钥不进沙箱)
	Timeout  time.Duration // 服务端按配置上限收敛
	MemoryMB int64
	CPUCores float64
	Network  bool // 首期恒 false(断网);实现侧须兜底强制
}

// SandboxResult 执行结果。非零退出码不是 error(脚本失败是合法结果,LLM 需看到 stderr);
// 超时/资源超限/基础设施故障才是 error。
type SandboxResult struct {
	ExitCode int
	Stdout   string // 截断至实现配置上限
	Stderr   string // 同上
	Duration time.Duration
}

// SandboxExecutor 沙箱执行器窄接口。
type SandboxExecutor interface {
	Run(ctx context.Context, spec SandboxSpec) (SandboxResult, error)
}
