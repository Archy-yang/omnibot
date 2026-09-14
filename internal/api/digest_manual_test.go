package api

import (
	"context"
	"os"
	"strconv"
	"testing"

	"omnibot/pkg/config"
)

// TestManualDigestRun 手动触发沉淀管线(运维工具,默认跳过)。
//
// 用途:积压清理/管线排查。正常路径不需要它——轮次收尾 NotifyTurn 自动触发;
// 只有"管线曾长期静默导致积压"或"想立即看到沉淀结果"时手动跑。
//
// 运行(会对该用户真实执行一轮沉淀:读区间 → LLM 对账 → 落库 → 推水位):
//
//	MANUAL_DIGEST_USER=1 go test ./internal/api -run TestManualDigestRun -v -timeout 10m
//
// 注意:须与线上服务共用同一数据库;服务进程若同时在对话会并发触发,
// per-user 单飞是进程内的,跨进程不互斥——低风险(水位原子推进)但避免高峰期跑。
func TestManualDigestRun(t *testing.T) {
	userIDStr := os.Getenv("MANUAL_DIGEST_USER")
	if userIDStr == "" {
		t.Skip("设 MANUAL_DIGEST_USER=<user_id> 以手动执行一轮沉淀(真实配置与数据库)")
	}
	userID, err := strconv.ParseInt(userIDStr, 10, 64)
	if err != nil || userID <= 0 {
		t.Fatalf("MANUAL_DIGEST_USER 无效: %q", userIDStr)
	}

	// 配置路径:环境变量 MANUAL_DIGEST_CONFIG 覆盖,缺省取仓库根的 configs/config.yaml
	// (go test 的 CWD 是本包目录,默认相对路径解析不到)。
	configPath := os.Getenv("MANUAL_DIGEST_CONFIG")
	if configPath == "" {
		configPath = "../../configs/config.yaml"
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatalf("加载配置(%s): %v", configPath, err)
	}
	deps := buildAppDeps(cfg)
	if deps.digestPipeline == nil {
		t.Skip("沉淀管线未启用(memory.extraction.enabled=false)")
	}

	if err := deps.digestPipeline.RunOnce(context.Background(), userID); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	t.Logf("用户 %d 沉淀一轮完成(单轮块上限内;若仍有积压,再跑一次本命令)", userID)
}
