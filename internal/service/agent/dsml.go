package agent

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// dsmlTag 是 DeepSeek DSML 的分隔符:全角竖线 U+FF5C 包裹 "DSML",即 (｜)DSML(｜)。
// 实测(task49)标签形如:开标签 "<dsmlTag+"invoke name=...>"、闭标签 "</dsmlTag+"invoke>"。
// 用 ｜ 转义显式锁死 U+FF5C(而非依赖源码里肉眼难辨的全角字符),确保与实际落库数据一致。
const dsmlTag = string(rune(0xFF5C)) + "DSML" + string(rune(0xFF5C))

// DeepSeek DSML(tool-call in prose)兜底解析。
//
// 背景:deepseek-v4-flash 经千帆在"思考模式/工具切换"边界时,会把工具调用以 DSML 标记
// 写进 delta.content(而非标准 tool_calls 数组)。若只简单 content += delta,这些标记就
// 原样成为最终文本(task49:子 Agent 的"最终报告"是一段被当成散文的 DSML 工具调用草稿)。
//
// 实测 task49 落库的真实结构(经转义还原):外层是 <dsmlTag>tool_calls 开闭标签包裹,
// 内部是 <dsmlTag>invoke name="X" 块,invoke 体内是 <dsmlTag>parameter name="k" string="true">
// 的 name/value 对。分隔符用 U+FF5C(全角竖线)+ "DSML"，避免与普通文本冲突。
//
// 本模块在这些标记进入业务文本前,把它们识别、解析回结构化 ToolCall,并从 content 剥离,
// 使上层 ReAct 循环能"真正执行"这些工具(而非当散文处理),同时不向用户/记录泄漏 DSML。
// 为稳健也兼容无分隔符的简单形态(<invoke name=...>...</invoke>)。
//
// 谨慎:DSML 是 DeepSeek 内部实现细节,官方不保证格式稳定。本层仅作故障恢复兜底,
// 不作为主要工具调用机制;标准 tool_calls 通道仍优先。
var (
	// dsmlTagRe 是 dsmlTag 的正则转义版(QuoteMeta),避免全角竖线等字符被当正则元字符。
	dsmlTagRe = regexp.QuoteMeta(dsmlTag)
	// `<dsmlTag>invoke name="..."` 块(分隔符可选,兼容 <invoke>/<｜DSML｜invoke> 两种形态)。
	dsmlInvokeRe = regexp.MustCompile(`(?s)<(?:` + dsmlTagRe + `)?invoke name="([^"]+)"[^>]*>(.*?)</(?:` + dsmlTagRe + `)?invoke>`)
	// `<dsmlTag>parameter name="..." ...>` 值 `</dsmlTag>parameter`。
	dsmlParamRe = regexp.MustCompile(`(?s)<(?:` + dsmlTagRe + `)?parameter name="([^"]+)"([^>]*)>(.*?)</(?:` + dsmlTagRe + `)?parameter>`)
	// 残留的外层包裹标签(tool_calls/tool_call),从清理文本里去掉。
	dsmlResidueRe = regexp.MustCompile(`(?s)</?(?:` + dsmlTagRe + `)?(?:tool_calls|tool_call)>`)
)

// parseDSML 从 content 识别 DSML 工具调用标记,提取为 []ToolCallDelta(Index/ID/Name/ArgumentsDelta),
// 并把标记从文本剥离。返回提取的工具与清理后的剩余文本。
// 无 DSML 标记时返回 (nil, 原content)。解析不出完整 invoke 块时宁可当文本透传(不破坏内容),
// 这是 best-effort 兜底,不以丢失信息为代价。
func parseDSML(content string) (_ []ToolCallDelta, cleanText string) {
	if !strings.Contains(content, dsmlTag) && !strings.Contains(content, "<invoke") {
		return nil, content
	}
	var (
		tools   []ToolCallDelta
		builder strings.Builder
		last    int
	)
	for _, m := range dsmlInvokeRe.FindAllStringSubmatchIndex(content, -1) {
		builder.WriteString(content[last:m[0]]) // invoke 之前的文本
		name := content[m[2]:m[3]]
		body := content[m[4]:m[5]]
		tools = append(tools, ToolCallDelta{
			Index:          len(tools),
			ID:             fmt.Sprintf("call_dsml_%d", len(tools)),
			Name:           name,
			ArgumentsDelta: buildDSMLArgs(body),
		})
		last = m[1]
	}
	builder.WriteString(content[last:])
	clean := dsmlResidueRe.ReplaceAllString(builder.String(), " ")
	return tools, strings.TrimSpace(clean)
}

// buildDSMLArgs 把 invoke 体内的 parameter 块解析成 arguments JSON 字符串。
// 按 parameter 的 string/number/boolean 属性生成对应类型值 --- 与标准 tool_call arguments 同构,
// 供上层 parseToolCall 直接使用。
func buildDSMLArgs(body string) string {
	var (
		order []string
		vals  = map[string]interface{}{}
	)
	for _, m := range dsmlParamRe.FindAllStringSubmatch(body, -1) {
		name := m[1]
		attrs := m[2]
		raw := strings.TrimSpace(m[3])
		var val interface{} = raw
		switch {
		case strings.Contains(attrs, `number="true"`):
			if f, err := strconv.ParseFloat(raw, 64); err == nil {
				val = f
			}
		case strings.Contains(attrs, `boolean="true"`):
			val = raw == "true"
		}
		if _, exists := vals[name]; !exists {
			order = append(order, name)
		}
		vals[name] = val
	}
	parts := make([]string, 0, len(order))
	for _, k := range order {
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(vals[k])
		parts = append(parts, string(kb)+":"+string(vb))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
