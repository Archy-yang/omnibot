package api

import (
	"context"
	"os"
	"strconv"
	"testing"

	"omnibot/pkg/config"
)

// TestManualContextBuild 手动冒烟 Context Manager(运维工具,默认跳过)。
//
// 用途:验证 Compact 触发/水位推进/上下文组装在真实数据库上的表现。
// 可能触发一次真实 LLM 压缩调用(中段积压超阈值时),属系统正常行为。
//
//	MANUAL_CONTEXT_BUILD=1 go test ./internal/api -run TestManualContextBuild -v
func TestManualContextBuild(t *testing.T) {
	if os.Getenv("MANUAL_CONTEXT_BUILD") == "" {
		t.Skip("设 MANUAL_CONTEXT_BUILD=1 以对真实数据库冒烟 Context Manager")
	}
	userID := int64(1)
	if v := os.Getenv("MANUAL_CONTEXT_USER"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			t.Fatalf("MANUAL_CONTEXT_USER 无效: %q", v)
		}
		userID = id
	}

	configPath := os.Getenv("MANUAL_CONTEXT_CONFIG")
	if configPath == "" {
		configPath = "../../configs/config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("加载配置(%s): %v", configPath, err)
	}
	deps := buildAppDeps(cfg)

	msgs, err := deps.msgSvc.BuildContextMessages(context.Background(), userID, "（冒烟：上下文构建检查）")
	if err != nil {
		t.Fatalf("BuildContextMessages: %v", err)
	}

	totalTokens := 0
	for _, m := range msgs {
		totalTokens += len([]rune(m.Content))
	}
	t.Logf("上下文构建成功: %d 条消息,约 %d 字符", len(msgs), totalTokens)
	for i, m := range msgs[:min(5, len(msgs))] {
		t.Logf("  [%d] role=%s content=%d字 %.40s", i, m.Role, len([]rune(m.Content)), m.Content)
	}
}
