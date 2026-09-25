package toolcore

// toolcore_guard_test.go — 分层守护(DeepSeek 架构审查 §7.3 整改防回归):
// tool/mcp/agent-tools 不得 import service/agent(Tool 契约已下沉本包);
// 本包自身不得 import 任何 service 层包(中立契约包纯度)。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuard_NoAgentImportsInToolConsumers(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	banned := []string{
		"internal/service/tool",
		"internal/service/mcp",
		"internal/service/agent/tools",
	}
	for _, dir := range banned {
		files, err := filepath.Glob(filepath.Join(repoRoot, dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue // 测试允许构造桩(仍不建议,先不禁止)
			}
			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			if strings.Contains(string(b), `"omnibot/internal/service/agent"`) {
				t.Errorf("分层倒挂回归:%s 不得 import service/agent(工具契约已下沉 toolcore)", f)
			}
		}
	}
}

func TestGuard_ToolcoreIsNeutral(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "\"omnibot/internal/service/") ||
				strings.HasPrefix(trimmed, "omnibot/internal/service/") ||
				strings.Contains(trimmed, "_ \"omnibot/internal/service/") {
				t.Errorf("toolcore 必须保持中立:%s 引用了 service 层(%s)", f, trimmed)
			}
		}
	}
}
