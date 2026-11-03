package search

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// N5 结果格式化:模型可读文本、预算截断、空结果文案。

func TestFormatResults_Empty(t *testing.T) {
	out := FormatResults("冷门问题", nil, false)
	assert.Contains(t, out, "没有搜到")
	assert.Contains(t, out, "冷门问题")
	assert.Contains(t, out, "web_read")
}

func TestFormatResults_Normal(t *testing.T) {
	out := FormatResults("golang 泛型", []Result{{
		Title:    "Go 泛型指南",
		URL:      "https://go.example.com/generics",
		Summary:  "泛型在 1.18 引入……",
		SiteName: "Go 官方",
		Date:     "2026-06-01T00:00:00+00:00",
	}}, false)

	assert.Contains(t, out, "搜索「golang 泛型」· 1 条结果")
	assert.Contains(t, out, "1. Go 泛型指南")
	assert.Contains(t, out, "https://go.example.com/generics")
	assert.Contains(t, out, "摘要:泛型在 1.18 引入")
	assert.Contains(t, out, "来源:Go 官方 · 2026-06-01")
	assert.Contains(t, out, "web_read", "尾注提示读全文用 web_read")
}

func TestFormatResults_Fallbacks(t *testing.T) {
	out := FormatResults("q", []Result{{
		Title:     "",
		URL:       "https://only-url.example.com",
		Summary:   "",
		Snippet:   "回落到 snippet",
		SiteName:  "",
		Date:      "未知日期",
		Authority: 0,
	}}, false)

	assert.Contains(t, out, "1. https://only-url.example.com", "标题为空时用 URL")
	assert.Contains(t, out, "摘要:回落到 snippet")
	assert.Contains(t, out, "来源:未知日期", "日期解析失败原样保留")
}

func TestFormatResults_TruncatesLongSummary(t *testing.T) {
	long := strings.Repeat("中", 600)
	out := FormatResults("q", []Result{{Title: "t", URL: "https://a", Summary: long}}, false)

	assert.NotContains(t, out, strings.Repeat("中", 500), "摘要必须被截断")
	assert.Contains(t, out, "…")
}

func TestFormatResults_ContentOnlyWhenNeeded(t *testing.T) {
	results := []Result{{Title: "t", URL: "https://a", Summary: "s", Content: "正文内容ABC"}}

	without := FormatResults("q", results, false)
	assert.NotContains(t, without, "正文内容ABC")

	with := FormatResults("q", results, true)
	assert.Contains(t, with, "正文:正文内容ABC")
}

func TestFormatResults_RespectsTotalBudget(t *testing.T) {
	var results []Result
	for i := 0; i < 30; i++ {
		results = append(results, Result{
			Title:   strings.Repeat("标题", 20),
			URL:     "https://example.com/" + strings.Repeat("p", 60),
			Summary: strings.Repeat("摘要", 200),
		})
	}

	out := FormatResults("q", results, false)
	assert.Less(t, len(out), resultTotalBudget+200, "总预算必须兜住")
	assert.Contains(t, out, "已截断")
}
