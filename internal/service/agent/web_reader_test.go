package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWebReader_EmptyArgs(t *testing.T) {
	tool := CreateWebReaderTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
}

func TestWebReader_NonHTTPProtocol(t *testing.T) {
	tool := CreateWebReaderTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "file:///etc/passwd",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP")
}

func TestWebReader_LoopbackSSRF(t *testing.T) {
	tool := CreateWebReaderTool()
	// 目标 URL 是内网,validateURL 应在调 Jina 前拒绝(不泄漏内网到 Jina)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://127.0.0.1:8080/api/v1/health",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内网")
}

func TestWebReader_MetadataSSRF(t *testing.T) {
	tool := CreateWebReaderTool()
	// 云元数据地址,应被拒(防 LLM 被诱导抓元数据再经 Jina 外泄)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://169.254.169.254/latest/meta-data/",
	})
	require.Error(t, err)
}

// TestWebReader_Description 描述应含分工说明(让模型知道何时升级)
func TestWebReader_Description(t *testing.T) {
	tool := CreateWebReaderTool()
	assert.Equal(t, "web_reader", tool.Name)
	// 描述强调:web_fetcher 搞不定时用 / JS 渲染 / 复杂页面
	assert.Contains(t, tool.Description, "web_fetcher")
	assert.Contains(t, tool.Description, "JS")
}

// TestWebFetcher_Description web_fetcher 描述应提示搞不定换 web_reader
func TestWebFetcher_Description(t *testing.T) {
	tool := CreateWebFetcherTool()
	assert.Equal(t, "web_fetcher", tool.Name)
	assert.Contains(t, tool.Description, "web_reader")
}
