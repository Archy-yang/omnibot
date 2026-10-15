// Package textutil 提供安全的文本截断工具。
//
// 背景:PG 严格校验 UTF-8,而 Go 字符串切片按字节进行——s[:N] 切在多字节
// 字符中间会产出非法 UTF-8,落库即报 "invalid byte sequence for encoding
// UTF8"(SQLSTATE 22021)。所有需要截断用户可见/入库文本的地方必须用本包,
// 禁止直接 s[:N] + "..."。
package textutil

import "unicode/utf8"

// TruncateRunes 按 rune 截断 s 到最多 max 个字符,产出恒为合法 UTF-8。
// max <= 0 返回空串;不超长原样返回。
func TruncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	return string(runes[:max])
}

// TruncateUTF8Safe 按**字节**预算截断 s,但回退到合法 UTF-8 边界
// (最多丢弃末尾一个不完整字符,至多 utf8.UTFMax-1 字节)。
// 适用于字节预算语义的调用点(如 LLM 上下文正文上限)。
func TruncateUTF8Safe(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := s[:maxBytes]
	// 逐字节回退到合法 UTF-8 边界(最多丢 3 字节,即一个残缺字符)。
	// 不能只查 RuneStart:起始字节在、后续字节被切掉仍是非法序列。
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
