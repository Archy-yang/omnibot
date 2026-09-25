package chat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestEstimateTokens Phase 2(16-架构迭代路线图 §6.1):Context 预算的计量单位。
// CJK ≈1 token/字,ASCII ≈4 字符/token;量级正确即可,不追求分词器精度。
func TestEstimateTokens(t *testing.T) {
	assert.Zero(t, EstimateTokens(""))

	// 中文:1 字 ≈ 1 token
	assert.Equal(t, 5, EstimateTokens("你好世界呀"))

	// ASCII:4 字符 ≈ 1 token(16 字符 → 4)
	assert.Equal(t, 4, EstimateTokens("abcdefghijklmnop"))

	// 混合:2 CJK + 8 ASCII = 2 + 2
	assert.Equal(t, 4, EstimateTokens("你好abcdefgh"))

	// 量级检查:一段典型中文对话文本,估算值与真实 BPE 同量级(不差 5 倍以上)
	long := strings.Repeat("今天我们讨论一下 Context 管理的设计。", 100)
	est := EstimateTokens(long)
	assert.Greater(t, est, 1000, "长文本估算不应过小")
	assert.Less(t, est, 20000, "长文本估算不应离谱放大")
}
