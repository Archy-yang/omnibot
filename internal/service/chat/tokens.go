package chat

import "unicode"

// EstimateTokens 轻量 token 估算(Phase 2,16-架构迭代路线图 §6.1)。
//
// 不引入分词器依赖:CJK 字符(中/日/韩)按 1 token/字计,其余(拉丁/数字/符号/空白)
// 按 4 字符 ≈ 1 token 计。对现代 BPE 分词器的中文(约 0.6-1.5 token/字)与英文
// (约 4 字符/token)都是可接受的近似——Context 预算只需要量级正确,不需要精确。
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk, other := 0, 0
	for _, r := range text {
		if isCJK(r) {
			cjk++
		} else {
			other++
		}
	}
	return cjk + other/4
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) || // CJK 统一表意文字
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}
