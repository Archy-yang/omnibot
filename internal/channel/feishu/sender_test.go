package feishu

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuildMarkdownCard_V2Structure 纯正文卡片应为 JSON 2.0 结构:
// schema="2.0" + body.elements[markdown],不含 1.0 的顶层 elements/config.wide_screen_mode。
func TestBuildMarkdownCard_V2Structure(t *testing.T) {
	card := buildMarkdownCard("**正文内容**")

	bs, err := json.Marshal(card)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(bs, &m))

	// 2.0 标识
	assert.Equal(t, "2.0", m["schema"])
	// body.elements 存在,且是 markdown 元素
	body, ok := m["body"].(map[string]any)
	require.True(t, ok)
	elements, ok := body["elements"].([]any)
	require.True(t, ok)
	require.Len(t, elements, 1)
	md := elements[0].(map[string]any)
	assert.Equal(t, "markdown", md["tag"])
	assert.Equal(t, "**正文内容**", md["content"])
	// 不应含 1.0 的顶层 elements / config.wide_screen_mode
	_, hasTopElements := m["elements"]
	assert.False(t, hasTopElements, "不应有 1.0 顶层 elements")
	assert.Nil(t, m["header"], "纯正文卡片不应有 header")
}

// TestBuildCard_V2Structure 带 header 卡片应为 JSON 2.0 结构:
// schema + header(title plain_text + template) + body.elements[markdown]。
func TestBuildCard_V2Structure(t *testing.T) {
	card := buildCard("📋 子任务完成汇报", "任务结果正文", "blue")

	bs, err := json.Marshal(card)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(bs, &m))

	assert.Equal(t, "2.0", m["schema"])

	// header: title(plain_text + content) + template
	header, ok := m["header"].(map[string]any)
	require.True(t, ok)
	title, ok := header["title"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "plain_text", title["tag"])
	assert.Equal(t, "📋 子任务完成汇报", title["content"])
	assert.Equal(t, "blue", header["template"])

	// body.elements[markdown]
	body, ok := m["body"].(map[string]any)
	require.True(t, ok)
	elements, ok := body["elements"].([]any)
	require.True(t, ok)
	require.Len(t, elements, 1)
	md := elements[0].(map[string]any)
	assert.Equal(t, "markdown", md["tag"])
	assert.Equal(t, "任务结果正文", md["content"])
}

// TestBuildCard_EmptyTemplateOmitsField template 空时不应出现 template 字段(飞书用默认色)。
func TestBuildCard_EmptyTemplateOmitsField(t *testing.T) {
	card := buildCard("标题", "正文", "")

	bs, err := json.Marshal(card)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(bs, &m))

	header, ok := m["header"].(map[string]any)
	require.True(t, ok)
	_, hasTemplate := header["template"]
	assert.False(t, hasTemplate, "template 空时应省略该字段")
}
