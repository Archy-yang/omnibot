package search

// deepseek.go — DeepSeek Search API 适配器(32 号 §3/§4.4)。
//
// 契约(POST {base_url}/v1/tools/search):
//
//	请求 {"query","count","need_content"}  Authorization: Bearer <key>
//	响应 {"results":[{title,url,snippet,summary,content,site_name,date,rerank_score,authority}]}
//
// 错误遵循 OpenAI 兼容格式,常见 401(密钥)/402(余额)/429(限流)/5xx。
// 本文件是唯一知道三方协议的地方——换服务商只加一个 Provider 实现。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	domain "omnibot/internal/domain/search"
)

const (
	// maxSearchRespBytes 响应体上限,防异常大包打爆内存。
	maxSearchRespBytes = 4 << 20
	// maxErrMessageRunes 三方错误信息截断长度(只进日志,不回模型)。
	maxErrMessageRunes = 200
)

type deepSeekProvider struct {
	client *http.Client
}

// NewDeepSeekProvider 生产实现。超时由每次请求的 ctx 控制(取用户配置),
// 故 http.Client 不设全局 Timeout。
func NewDeepSeekProvider() Provider {
	return &deepSeekProvider{client: &http.Client{}}
}

type deepSeekRequest struct {
	Query       string `json:"query"`
	Count       int    `json:"count"`
	NeedContent bool   `json:"need_content"`
}

type deepSeekResponse struct {
	Results []Result `json:"results"`
}

func (p *deepSeekProvider) Search(ctx context.Context, cfg *domain.Config, apiKey string, req Request) ([]Result, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = domain.DefaultTimeoutSeconds * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	payload, err := json.Marshal(deepSeekRequest{
		Query:       req.Query,
		Count:       req.Count,
		NeedContent: req.NeedContent,
	})
	if err != nil {
		return nil, fmt.Errorf("搜索请求构造失败")
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.DefaultEndpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("搜索请求构造失败")
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", "OmniBot/1.0 (web_search)")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		// 保留 %w 让上层用 errors.Is 判定超时/取消;文案由工具层统一转写,不透传内部地址。
		return nil, fmt.Errorf("搜索服务请求失败:%w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxSearchRespBytes))
	if err != nil {
		return nil, fmt.Errorf("搜索服务响应读取失败")
	}

	if resp.StatusCode != http.StatusOK {
		return nil, &StatusError{Code: resp.StatusCode, Message: extractProviderError(body)}
	}

	var parsed deepSeekResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		// 不把原始 body 带进错误:可能含内部字段,且模型只该看到行动建议。
		return nil, fmt.Errorf("搜索服务返回了无法解析的数据")
	}
	return parsed.Results, nil
}

// extractProviderError 从 OpenAI 兼容错误体里取 message(仅用于日志排查)。
func extractProviderError(body []byte) string {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Error.Message == "" {
		return ""
	}
	return truncateRunes(strings.TrimSpace(payload.Error.Message), maxErrMessageRunes)
}
