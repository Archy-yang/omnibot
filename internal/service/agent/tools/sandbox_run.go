package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"omnibot/internal/domain/sandbox"
	"omnibot/internal/pkg/toolcore"
)

// 沙箱脚本执行工具(17-脚本沙箱技术方案 §4):
// Agent 经由一次性容器沙箱执行 Python/Shell 脚本,完成数据处理/格式转换/计算验证。
// 子 Agent 专属(RegisterBuiltinSubOnly,对齐方向 B"管家不亲自干活")。
//
// 安全收口(方案 §6):断网/只读 rootfs/非 root/四重资源限额由执行器保证,本工具只做
// 参数解析与规格约束(Network 恒 false);结果以文本回传,非零退出码不是错误(LLM 需看 stderr)。

// CreateSandboxRunTool 构造 sandbox_run 工具,exec 由装配点注入(接口依赖,不感知 Docker)。
func CreateSandboxRunTool(exec sandbox.SandboxExecutor) toolcore.Tool {
	return toolcore.Tool{
		Name: "sandbox_run",
		Description: "在隔离沙箱中执行脚本(Python/Shell 等)。适合：数据处理、格式转换(JSON/CSV/文本批量加工)、" +
			"精确计算与验证、小段代码逻辑验证。用法：cmd 传命令数组，如 [\"python3\",\"main.py\"]；" +
			"脚本与数据用 files 传入(name 为工作目录下相对路径，如 main.py)；大段原始内容可走 stdin。" +
			"沙箱默认断网：需要联网信息时先经 web_read 获取，再以文件传入。结果以 stdout/stderr 文本回传" +
			"(有截断上限，大结果请分批输出)；脚本失败(非零退出码)会原样返回错误输出，可据此修正重试。" +
			"禁止把敏感信息写进脚本。",
		DisplayLabel: "执行了脚本",
		Capabilities: []string{toolcore.CapCode},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"cmd": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "命令及参数数组(argv 直传)。如 [\"python3\",\"main.py\"];多段逻辑请写成脚本文件经 files 传入再执行",
				},
				"files": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"name":    map[string]interface{}{"type": "string", "description": "相对路径,如 main.py 或 data/input.csv"},
							"content": map[string]interface{}{"type": "string", "description": "文件内容(文本)"},
						},
						"required": []string{"name", "content"},
					},
					"description": "可选。注入工作目录的输入文件(脚本/数据)",
				},
				"stdin": map[string]interface{}{
					"type":        "string",
					"description": "可选。通过标准输入传给命令的内容(适合大段原文,避免命令行转义)",
				},
				"timeout_seconds": map[string]interface{}{
					"type":        "number",
					"description": "可选。超时秒数(默认与上限见部署配置,超限自动收敛)",
				},
			},
			"required": []string{"cmd"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			spec, err := parseSandboxSpec(args)
			if err != nil {
				return "", err
			}
			res, err := exec.Run(ctx, spec)
			if err != nil {
				return "", err
			}
			return formatSandboxResult(res), nil
		},
	}
}

// parseSandboxSpec 解析 LLM 参数为 SandboxSpec(LLM 传来的是 []interface{}/float64)。
// MemoryMB/CPUCores 不暴露给 LLM(资源限额是部署者决策,由执行器配置兜底);Network 恒 false。
func parseSandboxSpec(args map[string]interface{}) (sandbox.SandboxSpec, error) {
	spec := sandbox.SandboxSpec{Network: false}

	rawCmd, ok := args["cmd"].([]interface{})
	if !ok || len(rawCmd) == 0 {
		return spec, fmt.Errorf("cmd 必须是非空字符串数组,如 [\"python3\",\"main.py\"]")
	}
	spec.Cmd = make([]string, 0, len(rawCmd))
	for _, v := range rawCmd {
		s, ok := v.(string)
		if !ok {
			return spec, fmt.Errorf("cmd 元素必须是字符串")
		}
		spec.Cmd = append(spec.Cmd, s)
	}

	if rawFiles, ok := args["files"].([]interface{}); ok {
		for _, fv := range rawFiles {
			fm, ok := fv.(map[string]interface{})
			if !ok {
				return spec, fmt.Errorf("files 元素必须是对象({name, content})")
			}
			name, _ := fm["name"].(string)
			content, _ := fm["content"].(string)
			spec.Files = append(spec.Files, sandbox.File{Name: name, Content: content})
		}
	}

	if s, ok := args["stdin"].(string); ok {
		spec.Stdin = s
	}

	if secs, ok := args["timeout_seconds"].(float64); ok && secs > 0 {
		spec.Timeout = time.Duration(secs * float64(time.Second))
	}
	return spec, nil
}

// formatSandboxResult 结果分节回传(退出码 + stdout/stderr),LLM 可按节读取。
func formatSandboxResult(res sandbox.SandboxResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "exit_code: %d", res.ExitCode)
	if res.Stdout != "" {
		b.WriteString("\n--- stdout ---\n")
		b.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		b.WriteString("\n--- stderr ---\n")
		b.WriteString(res.Stderr)
	}
	return b.String()
}
