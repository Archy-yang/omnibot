package search

// errors.go — 检索错误 → 面向模型的中文文案(32 号 §4.4)。
//
// 为什么集中在这里:状态码语义属于 provider,文案要跟它一起演进;
// 工具层只负责"取参数、取用户、把文案交出去",两处各改一半最容易漂。
//
// 原则:
//   - 配置/账户类问题(401/402/403/未配置)是用户要去改设置,给"去设置"的指引;
//   - 瞬时故障(429/超时/5xx)给"稍后或换路线"的指引,并明说可改用 web_read;
//   - 一律不透传三方原始响应体与内部地址。

import (
	"context"
	"errors"
	"fmt"
)

// UserMessage 把检索错误翻译成模型可读、可行动的文案(空错误返回空串)。
func UserMessage(err error) string {
	if err == nil {
		return ""
	}

	if errors.Is(err, ErrNotConfigured) {
		return "联网搜索还没配置或已关闭,暂时用不了。可以改用 web_read 读取已知页面,或如实告诉用户没查到。"
	}
	if errors.Is(err, ErrInvalidQuery) {
		return "查询词不合法(不能为空,且不超过 128 字)。请精简后重试。"
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return "搜索超时了。可以稍后再试,或改用 web_read 读取已知页面。"
	}

	var statusErr *StatusError
	if errors.As(err, &statusErr) {
		switch statusErr.Code {
		case 400:
			return "搜索服务不接受这次查询(查询词可能过长或含非法字符)。精简后重试。"
		case 401:
			return "搜索服务的密钥无效,需要用户在设置里重新填写 API Key。可先用 web_read 应对。"
		case 402:
			return "搜索服务的账户余额不足,暂时用不了。可先用 web_read,并提醒用户充值。"
		case 403:
			return "搜索服务拒绝了这次请求(可能触发了服务方的内容安全策略)。换 web_read 或如实告诉用户查不到。"
		case 429:
			return "搜索服务限流了,稍后再试,或先用 web_read 读取已知页面。"
		case 413:
			return "这次查询的请求体过大,精简查询词后重试。"
		case 504:
			return "搜索服务上游超时,稍后再试或改用 web_read。"
		}
		if statusErr.Code >= 500 {
			return "搜索服务暂时不可用,稍后再试或改用 web_read。"
		}
		return fmt.Sprintf("搜索服务返回了异常状态(%d),稍后再试或改用 web_read。", statusErr.Code)
	}

	return "搜索服务暂时不可用,稍后再试或改用 web_read。"
}
