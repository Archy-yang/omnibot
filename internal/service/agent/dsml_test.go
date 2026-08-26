package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// parseDSMLTestOpen/Close 构造 DSML 标签的前缀。
// 实测落库(经转义还原)的闭合标签带 '/'：开标签 <dsmlTag>invoke、闭标签 </dsmlTag>invoke。
func dsmlTestOpen() string  { return "<" + dsmlTag }
func dsmlTestClose() string { return "</" + dsmlTag }

// TestParseDSML_InvokeBlock 单个 invoke 块:应提取出工具+参数,并把标签从文本剥离。
// 用例严格还原 task 49 实际泄漏的 DSML 格式(全角竖线 U+FF5C 包裹 "DSML" 作分隔符,
// <dsmlTag>tool_calls 包裹 <dsmlTag>invoke + <dsmlTag>parameter)。
func TestParseDSML_InvokeBlock(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	content := O + "tool_calls>\n" +
		O + "invoke name=\"web_read\">" +
		O + "parameter name=\"url\" string=\"true\">https://a.com" +
		C + "parameter>" +
		C + "invoke>\n" +
		C + "tool_calls>"
	tools, clean := parseDSML(content)
	require.Len(t, tools, 1)
	assert.Equal(t, "web_read", tools[0].Name)
	assert.Equal(t, `{"url":"https://a.com"}`, tools[0].ArgumentsDelta)
	assert.NotEmpty(t, tools[0].ID)
	assert.Equal(t, 0, tools[0].Index)
	assert.NotContains(t, clean, "<", "DSML 标签应从文本剥离")
	assert.NotContains(t, clean, "DSML", "分隔符不应残留")
}

// TestParseDSML_MultipleInvocations 同块多个 invoke -> 提取多个工具,index 递增。
func TestParseDSML_MultipleInvocations(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	content := O + "tool_calls>" +
		O + "invoke name=\"web_read\">" + O + "parameter name=\"url\" string=\"true\">https://a" + C + "parameter>" + C + "invoke>" +
		O + "invoke name=\"web_read\">" + O + "parameter name=\"url\" string=\"true\">https://b" + C + "parameter>" + C + "invoke>" +
		C + "tool_calls>"
	tools, _ := parseDSML(content)
	require.Len(t, tools, 2)
	assert.Equal(t, `{"url":"https://a"}`, tools[0].ArgumentsDelta)
	assert.Equal(t, `{"url":"https://b"}`, tools[1].ArgumentsDelta)
	assert.Equal(t, 0, tools[0].Index)
	assert.Equal(t, 1, tools[1].Index)
}

// TestParseDSML_TypedParams 按 parameter 的 number/boolean 属性生成对应类型的 JSON 参数。
func TestParseDSML_TypedParams(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	content := O + "invoke name=\"get_weather_typed\">" +
		O + "parameter name=\"a\" number=\"true\">42" + C + "parameter>" +
		O + "parameter name=\"on\" boolean=\"true\">true" + C + "parameter>" +
		C + "invoke>"
	tools, _ := parseDSML(content)
	require.Len(t, tools, 1)
	assert.Equal(t, `{"a":42,"on":true}`, tools[0].ArgumentsDelta)
}

// TestParseDSML_Task49Origin 复现 task 49 落库的原始 DSML 块(两个 web_read invoke),
// 严格锁定真实格式:全角竖线分隔符 + tool_calls 包裹 + invoke/parameter 闭标签带 '/'。
func TestParseDSML_Task49Origin(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	content := O + "tool_calls>\n" +
		O + "invoke name=\"web_read\">\n" + O + "parameter name=\"url\" string=\"true\">https://www.chinahighlights.com/xishuangbanna/travel-guide.htm" + C + "parameter>\n" + C + "invoke>\n" +
		O + "invoke name=\"web_read\">\n" + O + "parameter name=\"url\" string=\"true\">https://baike.baidu.com/item/告庄西双景" + C + "parameter>\n" + C + "invoke>\n" +
		C + "tool_calls>"
	tools, clean := parseDSML(content)
	require.Len(t, tools, 2)
	assert.Equal(t, "web_read", tools[0].Name)
	assert.Equal(t, tools[0].ArgumentsDelta, `{"url":"https://www.chinahighlights.com/xishuangbanna/travel-guide.htm"}`)
	assert.Equal(t, tools[1].ArgumentsDelta, `{"url":"https://baike.baidu.com/item/告庄西双景"}`)
	assert.NotContains(t, clean, "invoke")
	assert.NotContains(t, clean, "tool_calls")
}

// TestParseDSML_NoDelimiterPlainInvoke 兼容无分隔符的简单形态(<invoke name=...> 老实现也覆盖)。
func TestParseDSML_NoDelimiterPlainInvoke(t *testing.T) {
	content := `<invoke name="web_read"><parameter name="url" string="true">https://a</parameter></invoke>`
	tools, clean := parseDSML(content)
	require.Len(t, tools, 1)
	assert.Equal(t, "web_read", tools[0].Name)
	assert.Equal(t, `{"url":"https://a"}`, tools[0].ArgumentsDelta)
	assert.NotContains(t, clean, "invoke")
}

// TestParseDSML_NoTags 普通文本无 DSML -> 不提取工具,文本原样保留。
func TestParseDSML_NoTags(t *testing.T) {
	content := "你好，这是一段正常回复。"
	tools, clean := parseDSML(content)
	assert.Len(t, tools, 0)
	assert.Equal(t, content, clean)
}

// TestParseDSML_ProseAroundInvoke 工具调用夹杂在散文里 -> 提取工具,散文保留。
func TestParseDSML_ProseAroundInvoke(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	content := "我要查这个" + O + "invoke name=\"web_read\">" + O + "parameter name=\"url\" string=\"true\">https://a" + C + "parameter>" + C + "invoke>然后汇总"
	tools, clean := parseDSML(content)
	require.Len(t, tools, 1)
	assert.Contains(t, clean, "我要查这个")
	assert.Contains(t, clean, "然后汇总")
	assert.NotContains(t, clean, "invoke", "DSML 标签应从散文剥离")
}

// TestChatCompletionStream_DSMLConvertedToToolCall 流式层:deepseek 把 DSML 塞进 delta.content 时,
// 客户端必须把它转成 ToolCallDelta(而非当作普通 content 泄漏),供上层 ReAct 循环真正执行工具。
func TestChatCompletionStream_DSMLConvertedToToolCall(t *testing.T) {
	O, C := dsmlTestOpen(), dsmlTestClose()
	dsml := O + "tool_calls>" +
		O + "invoke name=\"web_read\">" + O + "parameter name=\"url\" string=\"true\">https://a.com" + C + "parameter>" + C + "invoke>" +
		C + "tool_calls>"
	// DSML 含双引号(name="web_read")与分隔符,密放进 JSON 字符串必须转义,用 json.Marshal 生成合法值。
	dsmlJSON, err := json.Marshal(dsml)
	require.NoError(t, err)
	body := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":` + string(dsmlJSON) + `},"index":0}]}`,
		``,
		`data: {"choices":[{"finish_reason":"tool_calls","index":0,"delta":{}}]}`,
		``,
		`data: [DONE]`,
		``, ``,
	}, "\n")

	srv := streamingHTTPServer(t, body)
	defer srv.Close()

	client := NewOpenAILLMClient("test-key", srv.URL, "deepseek-v4", 5*time.Second)
	ch, err := client.ChatCompletionStream(context.Background(), nil, nil)
	require.NoError(t, err)

	chunks := drainChunks(t, ch)

	var tools []ToolCallDelta
	var junk strings.Builder
	for _, c := range chunks {
		if c.ToolCallDelta != nil {
			tools = append(tools, *c.ToolCallDelta)
		}
		if c.ContentDelta != "" {
			junk.WriteString(c.ContentDelta)
		}
	}
	require.Len(t, tools, 1, "DSML 应被转换为 tool_call,而非当文本泄漏")
	assert.Equal(t, "web_read", tools[0].Name)
	assert.Equal(t, `{"url":"https://a.com"}`, tools[0].ArgumentsDelta)
	assert.Empty(t, strings.TrimSpace(junk.String()), "DSML 不应对用户/记录暴露为普通 content")
}
