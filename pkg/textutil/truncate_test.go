package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
)

// 截断安全性:PG 严格校验 UTF-8,按字节切片切在多字节字符中间会产生非法序列,
// 落库即报 "invalid byte sequence for encoding UTF8"(教训:agent_steps CreateBatch 被拒)。

// TruncateRunes:按 rune 截断,永不产出非法 UTF-8;中文串必须整字符保留。
func TestTruncateRunes(t *testing.T) {
	s := strings.Repeat("查", 100) // 300 字节,100 rune

	got := TruncateRunes(s, 50)
	assert.True(t, utf8.ValidString(got), "截断结果必须是合法 UTF-8")
	assert.Len(t, []rune(got), 50, "rune 数正确")
	assert.Equal(t, strings.Repeat("查", 50), got)

	// 不超长原样返回
	assert.Equal(t, s, TruncateRunes(s, 100))
	assert.Equal(t, s, TruncateRunes(s, 200))

	// ASCII 语义不变
	assert.Equal(t, "abc", TruncateRunes("abcdef", 3))

	// 边界:恰好等于 max 不截
	assert.Equal(t, "查查查", TruncateRunes("查查查", 3))

	// 混合中英:切点落在 ASCII 段也合法
	mixed := "中文abc中文"
	assert.True(t, utf8.ValidString(TruncateRunes(mixed, 5)))
}

// TruncateUTF8Safe:保持字节预算语义,但回退到合法 UTF-8 边界,最多丢弃末尾不完整字符。
func TestTruncateUTF8Safe(t *testing.T) {
	s := strings.Repeat("查", 100) // 每字 3 字节

	got := TruncateUTF8Safe(s, 200) // 200 = 66 字整 + 2 字节残片
	assert.True(t, utf8.ValidString(got), "截断结果必须是合法 UTF-8")
	assert.LessOrEqual(t, len(got), 200, "不超字节预算")
	assert.Equal(t, strings.Repeat("查", 66), got, "残片整字丢弃")

	// ASCII:预算用满,语义与纯字节切片一致
	assert.Equal(t, strings.Repeat("a", 100), TruncateUTF8Safe(strings.Repeat("a", 200), 100))

	// 不超长原样返回
	assert.Equal(t, s, TruncateUTF8Safe(s, 300))
	assert.Equal(t, s, TruncateUTF8Safe(s, 301))

	// 空串与 0 预算
	assert.Empty(t, TruncateUTF8Safe("abc", 0))
	assert.Empty(t, TruncateUTF8Safe("", 10))
}
