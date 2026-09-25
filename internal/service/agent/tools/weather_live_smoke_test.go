package tools

import (
	"context"
	"os"
	"testing"
)

// 手动 live smoke:打真站 NMC。跑法: LIVE_SMOKE=1 go test ./internal/service/agent/tools/ -run TestLiveWeatherSmoke -v
// 未设 LIVE_SMOKE 时跳过(go test ./... 不打真站)。
func TestLiveWeatherSmoke(t *testing.T) {
	if os.Getenv("LIVE_SMOKE") == "" {
		t.Skip("需要 LIVE_SMOKE=1(打真站)")
	}
	tool := CreateWeatherTool()
	out, err := tool.Execute(context.Background(), map[string]interface{}{"city": "北京"})
	if err != nil {
		t.Fatalf("live smoke failed: %v", err)
	}
	t.Logf("北京:\n%s", out)

	out2, err := tool.Execute(context.Background(), map[string]interface{}{"city": "朝阳", "province": "辽宁省"})
	if err != nil {
		t.Fatalf("live smoke(消歧) failed: %v", err)
	}
	t.Logf("辽宁朝阳:\n%s", out2)
}
