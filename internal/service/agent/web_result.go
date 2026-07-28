package agent

import (
	"fmt"
	"strings"
)

// web 工具(web_fetcher / web_reader)返回给 LLM 的统一标记文本格式。
//
// 设计目的:把 HTTP code、URL、原因、收敛建议统一成结构化文本,让 ReAct 模型在
// 「是否再次调工具」决策点能读懂失败原因并据此收敛(见 task 16:模型看到"读取服务返回
// HTTP 401"无从判断,反复换 URL 重试 19 轮)。
//
// 失败仍返回 error(保持 agent_steps 的 status=error 准确),error 内容即标记文本。
// ReAct 会拼成"工具执行错误: <标记文本>"喂回 LLM,模型据此决定放弃还是换路子。
//
// 成功返回标记文本字符串:头(HTTP code + URL)+ 裸 Markdown 正文,正文不被包装污染。
//
// 格式:
//   [抓取失败] HTTP 401
//   URL: https://www.volcengine.com/activity
//   原因: 目标站点要求登录鉴权
//   建议: 不要再试同站点其他页面;换用 search_memories/search_history 或基于已有信息汇总
//
//   [抓取成功] HTTP 200
//   URL: https://blog.example.com/post
//   正文:
//   <Markdown 正文>

// formatWebFailure 构造失败标记文本 error。httpCode=0 表示无 HTTP 状态(请求未发出/连接级错误),省略 HTTP 行。
func formatWebFailure(httpCode int, url, reason string) error {
	var b strings.Builder
	b.WriteString("[抓取失败]")
	if httpCode > 0 {
		fmt.Fprintf(&b, " HTTP %d", httpCode)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "URL: %s\n", url)
	fmt.Fprintf(&b, "原因: %s\n", reason)
	fmt.Fprintf(&b, "建议: %s", webFailureSuggestion(httpCode))
	return fmt.Errorf("%s", b.String())
}

// formatWebSuccess 构造成功标记文本:头 + 裸正文。
func formatWebSuccess(httpCode int, url, content string) string {
	var b strings.Builder
	b.WriteString("[抓取成功]")
	if httpCode > 0 {
		fmt.Fprintf(&b, " HTTP %d", httpCode)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "URL: %s\n", url)
	b.WriteString("正文:\n")
	b.WriteString(content)
	return b.String()
}

// webFailureSuggestion 按 HTTP code 给收敛建议。401/403 鉴权失败 -> 放弃同站点;
// 其他(超时/404/500/连接错误) -> 换来源或基于已有信息汇总。
func webFailureSuggestion(httpCode int) string {
	if httpCode == 401 || httpCode == 403 {
		return "目标站点要求登录鉴权,该站点页面普遍抓不到;不要再试同站点其他页面,换用 search_memories/search_history 或基于已有信息汇总"
	}
	return "该 URL 无法获取有效内容,换用其他来源或基于已有信息汇总,不要反复重试同类失败"
}
