package agent

import (
	"strings"
	"testing"
)

// TestFormatWebFailure 统一的 web 工具失败返回格式:标记文本 error。
// 模型从错误里能读到 HTTP code + URL + 原因 + 建议(收敛提示),据此决定是否放弃。
func TestFormatWebFailure(t *testing.T) {
	got := formatWebFailure(401, "https://www.volcengine.com/activity", "目标站点要求登录鉴权")
	s := got.Error()
	assertContains(t, s, "[抓取失败]")
	assertContains(t, s, "HTTP 401")
	assertContains(t, s, "https://www.volcengine.com/activity")
	assertContains(t, s, "原因: 目标站点要求登录鉴权")
	// 收敛提示:401/403 这种鉴权失败,告诉模型别再试同站点
	assertContains(t, s, "建议:")
	assertContains(t, s, "不要再试同站点")
}

// TestFormatWebFailure_NonHTTP 不带 HTTP code 的失败(SSRF/协议错误)。
// httpCode=0 表示无 HTTP 状态(请求未发出或连接级错误),格式应省略 HTTP 行,只给原因。
func TestFormatWebFailure_NonHTTP(t *testing.T) {
	got := formatWebFailure(0, "http://127.0.0.1/", "禁止访问内网或保留地址")
	s := got.Error()
	assertContains(t, s, "[抓取失败]")
	assertNotContains(t, s, "HTTP 0") // 0 不该显示成 HTTP 0
	assertContains(t, s, "原因: 禁止访问内网或保留地址")
}

// TestFormatWebFailure_SuggestionForAuth 401/403 给"放弃同站点"建议;
// 其他码(超时/404/500)给更通用的"换思路或基于已有信息汇总"建议。
func TestFormatWebFailure_SuggestionForAuth(t *testing.T) {
	auth := formatWebFailure(403, "u", "要登录").Error()
	assertContains(t, auth, "不要再试同站点")

	other := formatWebFailure(500, "u", "服务异常").Error()
	assertNotContains(t, other, "不要再试同站点")
	assertContains(t, other, "换用其他来源或基于已有信息汇总")
}

// TestFormatWebSuccess 成功返回标记文本:头(状态+code+URL)+ 裸 Markdown 正文。
// 正文不被任何 JSON/包装污染,保持 LLM 可读性。
func TestFormatWebSuccess(t *testing.T) {
	got := formatWebSuccess(200, "https://blog.example.com/post", "火山引擎 CodingPlan 首月9.9元")
	assertContains(t, got, "[抓取成功]")
	assertContains(t, got, "HTTP 200")
	assertContains(t, got, "https://blog.example.com/post")
	// 正文紧随,无包装
	assertContains(t, got, "火山引擎 CodingPlan 首月9.9元")
	// 正文前有明确分隔
	assertContains(t, got, "正文:")
}

func assertContains(t *testing.T, s, sub string) {
	t.Helper()
	if !strings.Contains(s, sub) {
		t.Errorf("expected %q to contain %q", s, sub)
	}
}

func assertNotContains(t *testing.T, s, sub string) {
	t.Helper()
	if strings.Contains(s, sub) {
		t.Errorf("expected %q to NOT contain %q", s, sub)
	}
}
