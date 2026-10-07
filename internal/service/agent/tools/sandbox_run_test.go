package tools

import (
	"context"
	"strings"
	"testing"

	"omnibot/internal/domain/sandbox"
)

// fakeSandboxExecutor 捕获 Spec、返回预定结果。
type fakeSandboxExecutor struct {
	gotSpec sandbox.SandboxSpec
	res     sandbox.SandboxResult
	err     error
}

func (f *fakeSandboxExecutor) Run(_ context.Context, spec sandbox.SandboxSpec) (sandbox.SandboxResult, error) {
	f.gotSpec = spec
	return f.res, f.err
}

// T1 工具定义:名称/必填参数/描述声明断网与 stdout 回传。
func TestSandboxRunTool_Definition(t *testing.T) {
	tool := CreateSandboxRunTool(&fakeSandboxExecutor{})
	if tool.Name != "sandbox_run" {
		t.Errorf("Name = %q", tool.Name)
	}
	if tool.DisplayLabel == "" {
		t.Error("DisplayLabel 不应为空(流式状态条)")
	}
	props, ok := tool.Parameters["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("Parameters 缺 properties")
	}
	if _, ok := props["cmd"]; !ok {
		t.Error("缺 cmd 参数")
	}
	if _, ok := props["files"]; !ok {
		t.Error("缺 files 参数")
	}
	if _, ok := props["stdin"]; !ok {
		t.Error("缺 stdin 参数")
	}
	if _, ok := props["timeout_seconds"]; !ok {
		t.Error("缺 timeout_seconds 参数")
	}
	if strings.Contains(tool.Description, "可联网") {
		t.Error("描述不应暗示可联网")
	}
	if !strings.Contains(tool.Description, "断网") {
		t.Error("描述应声明断网约束")
	}
}

// T2 参数解析:LLM 传来的 []interface{}/float64 转为 Spec(argv 直传、文件、stdin、超时)。
func TestSandboxRunTool_ParsesArgs(t *testing.T) {
	exec := &fakeSandboxExecutor{res: sandbox.SandboxResult{ExitCode: 0, Stdout: "ok"}}
	tool := CreateSandboxRunTool(exec)
	args := map[string]interface{}{
		"cmd": []interface{}{"python3", "main.py"},
		"files": []interface{}{
			map[string]interface{}{"name": "main.py", "content": "print(1)"},
		},
		"stdin":           "raw input",
		"timeout_seconds": float64(30),
	}
	_, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	spec := exec.gotSpec
	if len(spec.Cmd) != 2 || spec.Cmd[0] != "python3" || spec.Cmd[1] != "main.py" {
		t.Errorf("Cmd = %v", spec.Cmd)
	}
	if len(spec.Files) != 1 || spec.Files[0].Name != "main.py" || spec.Files[0].Content != "print(1)" {
		t.Errorf("Files = %+v", spec.Files)
	}
	if spec.Stdin != "raw input" {
		t.Errorf("Stdin = %q", spec.Stdin)
	}
	if spec.Timeout != 30e9 {
		t.Errorf("Timeout = %v, want 30s", spec.Timeout)
	}
	if spec.Network {
		t.Error("Network 必须恒为 false(断网)")
	}
}

// T3 结果格式:退出码 + stdout/stderr 分节回传;非零退出码不返回 error。
func TestSandboxRunTool_ResultFormat(t *testing.T) {
	exec := &fakeSandboxExecutor{res: sandbox.SandboxResult{ExitCode: 2, Stdout: "部分输出", Stderr: "报错信息"}}
	tool := CreateSandboxRunTool(exec)
	out, err := tool.Execute(context.Background(), map[string]interface{}{"cmd": []interface{}{"python3", "-c", "x"}})
	if err != nil {
		t.Fatalf("非零退出码不应返回 error: %v", err)
	}
	if !strings.Contains(out, "exit_code: 2") || !strings.Contains(out, "部分输出") || !strings.Contains(out, "报错信息") {
		t.Errorf("结果格式不完整: %q", out)
	}
}

// T4 非法参数:空 cmd / 文件路径逃逸 → 报错且不触达执行器。
func TestSandboxRunTool_RejectsInvalidArgs(t *testing.T) {
	exec := &fakeSandboxExecutor{}
	tool := CreateSandboxRunTool(exec)

	if _, err := tool.Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Error("缺 cmd 应报错")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{
		"cmd":   []interface{}{},
		"files": []interface{}{map[string]interface{}{"name": "../x", "content": "y"}},
	}); err == nil {
		t.Error("非法文件路径应报错")
	}
	if exec.gotSpec.Cmd != nil {
		t.Error("非法参数不应触达执行器")
	}
}

// T5 执行器错误透传(Docker 不可用引导、超时),信息原样可读。
func TestSandboxRunTool_PassesThroughExecutorError(t *testing.T) {
	exec := &fakeSandboxExecutor{err: context.DeadlineExceeded}
	tool := CreateSandboxRunTool(exec)
	_, err := tool.Execute(context.Background(), map[string]interface{}{"cmd": []interface{}{"python3"}})
	if err == nil {
		t.Fatal("执行器错误应透传")
	}
}
