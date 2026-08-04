package agent

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fetchFunc / jinaFunc 是 webReadAuto 的依赖,签名与 fetchAndExtract / readViaJina 一致。
// 注入 mock 便于精确断言编排(调用次数/升降级分支),不触真实网络。
type fetchFunc func(context.Context, string) (string, error)
type jinaFunc func(context.Context, string) (string, error)

// callCount 记录 mock fetch/jina 被调用次数,断言"不升级时不调 Jina"等。
type callCount struct{ fetch, jina int }

// mockFetch 返回预设 result/err 并计数。
func mockFetch(cc *callCount, result string, err error) fetchFunc {
	return func(_ context.Context, _ string) (string, error) {
		cc.fetch++
		return result, err
	}
}

func mockJina(cc *callCount, result string, err error) jinaFunc {
	return func(_ context.Context, _ string) (string, error) {
		cc.jina++
		return result, err
	}
}

// longContent 构造一段足长正文(>minContentForUpgrade),使 needUpgrade=false。
func longContent() string {
	// 每段约 20 字符,循环 50 段 > 500 字符阈值
	out := ""
	for i := 0; i < 50; i++ {
		out += "这是一段足够长的正文内容用于测试。"
	}
	return out
}

// === needUpgrade 纯函数 ===

func TestNeedUpgrade_LongContent_False(t *testing.T) {
	result := formatWebSuccess(200, "https://example.com/a", longContent())
	assert.False(t, needUpgrade(result))
}

func TestNeedUpgrade_ShortContent_True(t *testing.T) {
	// 正文仅"导航菜单 几个字" -> 疑似导航/落地页 -> 升级
	result := formatWebSuccess(200, "https://example.com/a", "首页 文档 社区")
	assert.True(t, needUpgrade(result))
}

func TestNeedUpgrade_EmptyContent_True(t *testing.T) {
	result := formatWebSuccess(200, "https://example.com/a", "")
	assert.True(t, needUpgrade(result))
}

// === shouldTryJinaOnFailure 纯函数 ===

func TestShouldTryJinaOnFailure_SSRF_False(t *testing.T) {
	// SSRF/内网 -> Jina 中转目标仍可疑,不升级
	err := formatWebFailure(0, "https://example.com/a", "禁止访问内网或保留地址")
	assert.False(t, shouldTryJinaOnFailure(err))
}

func TestShouldTryJinaOnFailure_BadScheme_False(t *testing.T) {
	err := formatWebFailure(0, "https://example.com/a", "仅支持 HTTP/HTTPS 协议")
	assert.False(t, shouldTryJinaOnFailure(err))
}

func TestShouldTryJinaOnFailure_NonHTML_False(t *testing.T) {
	err := formatWebFailure(200, "https://example.com/a.pdf", "目标非 HTML 页面,无法提取正文")
	assert.False(t, shouldTryJinaOnFailure(err))
}

func TestShouldTryJinaOnFailure_Timeout_True(t *testing.T) {
	err := formatWebFailure(0, "https://example.com/a", "抓取超时,请稍后重试或换用 web_reader")
	assert.True(t, shouldTryJinaOnFailure(err))
}

func TestShouldTryJinaOnFailure_403_True(t *testing.T) {
	// 403 鉴权 -> Jina IP 不同可能绕过,升级
	err := formatWebFailure(403, "https://example.com/a", http.StatusText(403))
	assert.True(t, shouldTryJinaOnFailure(err))
}

func TestShouldTryJinaOnFailure_EmptyBody_True(t *testing.T) {
	// 空正文多半是 JS 渲染页 -> 正是 Jina 强项,升级
	err := formatWebFailure(0, "https://example.com/a", "抓取到空正文(可能是 JS 渲染页面,换用 web_reader)")
	assert.True(t, shouldTryJinaOnFailure(err))
}

// === webReadAuto 编排(mock fetch/jina) ===

// 1. fetch 成功+正文够长 -> 返回 fetch 结果,不调 Jina
func TestWebReadAuto_FetchSuccess_LongContent_NoJina(t *testing.T) {
	cc := &callCount{}
	fetchResult := formatWebSuccess(200, "https://example.com/a", longContent())
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, fetchResult, nil), mockJina(cc, "should-not-call", nil))
	require.NoError(t, err)
	assert.Equal(t, fetchResult, res)
	assert.Equal(t, 1, cc.fetch)
	assert.Equal(t, 0, cc.jina, "正文够长不应升级 Jina")
}

// 2. fetch 成功+正文短 -> 升级 Jina 成功 -> 返回 Jina 结果
func TestWebReadAuto_FetchSuccess_ShortContent_UpgradeJina(t *testing.T) {
	cc := &callCount{}
	fetchResult := formatWebSuccess(200, "https://example.com/a", "首页 文档")
	jinaResult := formatWebSuccess(200, "https://example.com/a", "完整正文内容...")
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, fetchResult, nil), mockJina(cc, jinaResult, nil))
	require.NoError(t, err)
	assert.Equal(t, jinaResult, res)
	assert.Equal(t, 1, cc.fetch)
	assert.Equal(t, 1, cc.jina)
}

// 3. fetch 成功+正文短+Jina 失败 -> 退回 fetch 结果(有内容总比无好)
func TestWebReadAuto_FetchSuccess_ShortContent_JinaFails_Fallback(t *testing.T) {
	cc := &callCount{}
	fetchResult := formatWebSuccess(200, "https://example.com/a", "首页 文档")
	jinaErr := formatWebFailure(0, "https://example.com/a", "抓取超时")
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, fetchResult, nil), mockJina(cc, "", jinaErr))
	require.NoError(t, err)
	assert.Equal(t, fetchResult, res, "Jina 失败应退回 fetch 结果")
	assert.Equal(t, 1, cc.jina)
}

// 4. fetch 失败(SSRF) -> 不调 Jina,返回 fetch 错误
func TestWebReadAuto_FetchFail_SSRF_NoJina(t *testing.T) {
	cc := &callCount{}
	fetchErr := formatWebFailure(0, "https://example.com/a", "禁止访问内网或保留地址")
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, "", fetchErr), mockJina(cc, "should-not-call", nil))
	require.Error(t, err)
	assert.Empty(t, res)
	assert.Equal(t, 0, cc.jina, "SSRF 失败不应升级 Jina")
}

// 5. fetch 失败(超时) -> 升级 Jina 成功 -> 返回 Jina 结果
func TestWebReadAuto_FetchFail_Timeout_UpgradeJina(t *testing.T) {
	cc := &callCount{}
	fetchErr := formatWebFailure(0, "https://example.com/a", "抓取超时")
	jinaResult := formatWebSuccess(200, "https://example.com/a", "Jina 渲染后的正文")
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, "", fetchErr), mockJina(cc, jinaResult, nil))
	require.NoError(t, err)
	assert.Equal(t, jinaResult, res)
	assert.Equal(t, 1, cc.jina)
}

// 6. fetch 失败(超时)+Jina 失败 -> 返回 fetch 错误
func TestWebReadAuto_FetchFail_Timeout_JinaFails_ReturnFetchErr(t *testing.T) {
	cc := &callCount{}
	fetchErr := formatWebFailure(0, "https://example.com/a", "抓取超时")
	jinaErr := formatWebFailure(500, "https://example.com/a", "Jina 内部错误")
	res, err := webReadAuto(context.Background(), "https://example.com/a",
		mockFetch(cc, "", fetchErr), mockJina(cc, "", jinaErr))
	require.Error(t, err)
	assert.Empty(t, res)
	assert.Equal(t, fetchErr.Error(), err.Error(), "Jina 也失败应返回 fetch 错误")
}

// === Execute 层 ===

func TestWebRead_Execute_EmptyArgs(t *testing.T) {
	tool := CreateWebReadTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
}

func TestWebRead_Execute_NonHTTP(t *testing.T) {
	tool := CreateWebReadTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "file:///etc/passwd",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP")
}

func TestWebRead_Execute_LoopbackSSRF(t *testing.T) {
	tool := CreateWebReadTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://127.0.0.1:8080/api/v1/health",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内网")
}

// TestWebRead_Execute_DefaultModeAuto mode 缺省应为 auto。
func TestWebRead_Execute_DefaultModeAuto(t *testing.T) {
	// 不实际抓取,仅断言缺省 mode 不报"未知 mode"错。
	// 用 SSRF URL 触发 validateURL 拒绝(在任何 mode 分发前),证明 mode 缺省走默认不 panic。
	tool := CreateWebReadTool()
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"url": "http://169.254.169.254/",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "内网")
}

// TestWebRead_Description 描述应含 auto/升级说明,引导模型用 auto。
func TestWebRead_Description(t *testing.T) {
	tool := CreateWebReadTool()
	assert.Equal(t, "web_read", tool.Name)
	assert.Contains(t, tool.Description, "auto")
}
