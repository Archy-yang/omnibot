package search

// service.go — 联网搜索的用户级配置服务 + 检索编排(32 号 §4.3/§4.4)。
//
// 职责边界:
//   - 配置:落库前校验(Normalize)、密钥加密、视图脱敏、留空保持;
//   - 检索:解析可用配置 → 调 provider → 拼装成模型可读文本。
// 三方协议细节全部收在 deepseek.go,本文件不出现任何 HTTP。

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	domain "omnibot/internal/domain/search"
	"omnibot/internal/pkg/crypto"
	"omnibot/pkg/textutil"
	repo "omnibot/internal/repository/search"
)

// 哨兵错误:调用方(工具层)据此映射成"可行动的中文文案",而不是把 err 抛给模型。
var (
	// ErrNotConfigured 未配置 / 已停用 / 密钥无法解密。
	ErrNotConfigured = errors.New("search: not configured")
	// ErrInvalidQuery 查询词为空或超长。
	ErrInvalidQuery = errors.New("search: invalid query")
)

// 格式化预算。摘要按"字"截(中文可读性),正文按"字节"截(PG 与 token 双约束)。
const (
	summaryMaxRunes   = 400
	contentMaxBytes   = 2000
	resultTotalBudget = 8000
)

// Request 一次检索请求(与三方协议解耦)。
type Request struct {
	Query       string
	Count       int
	NeedContent bool
}

// Result 单条检索结果(字段与主流搜索 API 对齐)。
type Result struct {
	Title       string  `json:"title"`
	URL         string  `json:"url"`
	Snippet     string  `json:"snippet"`
	Summary     string  `json:"summary"`
	Content     string  `json:"content"`
	SiteName    string  `json:"site_name"`
	Date        string  `json:"date"`
	RerankScore float64 `json:"rerank_score"`
	Authority   int     `json:"authority"`
}

// Provider 三方搜索适配器窄接口(测试可换成桩,不碰网络)。
type Provider interface {
	Search(ctx context.Context, cfg *domain.Config, apiKey string, req Request) ([]Result, error)
}

// StatusError 三方返回非 200 时的错误(带状态码,供上层映射文案)。
type StatusError struct {
	Code    int
	Message string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("search provider status %d: %s", e.Code, e.Message)
}

// ConfigView 面向 API 的配置视图(密钥一律脱敏)。
type ConfigView struct {
	HasConfig      bool   `json:"has_config"`
	Enabled        bool   `json:"enabled"`
	Provider       string `json:"provider"`
	BaseURL        string `json:"base_url"`
	APIKeyMasked   string `json:"api_key_masked"`
	DefaultCount   int    `json:"default_count"`
	NeedContent    bool   `json:"need_content"`
	TimeoutSeconds int    `json:"timeout_seconds"`
}

// SaveInput 保存配置的入参。
//
// APIKey 为空 = 保持原密钥(前端不回显明文);ClearKey = 显式清除。
// Enabled 为指针:区分"没传"(保持)与"传了 false"(关闭)。
type SaveInput struct {
	Provider       string
	BaseURL        string
	APIKey         string
	ClearKey       bool
	Enabled        *bool
	DefaultCount   int
	// NeedContent 指针:未传 = 保持原值(false 是有效业务值,不能用零值当"未提供")。
	NeedContent    *bool
	TimeoutSeconds int
}

// Service 配置服务 + 检索编排。
type Service struct {
	repo     repo.ConfigRepository
	provider Provider
}

// NewService 生产装配:DeepSeek provider。
func NewService(r repo.ConfigRepository) *Service {
	return &Service{repo: r, provider: NewDeepSeekProvider()}
}

// NewServiceWithProvider 测试/扩展装配。
func NewServiceWithProvider(r repo.ConfigRepository, p Provider) *Service {
	return &Service{repo: r, provider: p}
}

// GetConfig 取配置行(APIKey 仍是密文)。不存在、解密失败均返回 (nil, nil)。
// 解密失败按"未配置"降级:换过加密密钥的库不该让检索路径 panic 或泄露密文。
func (s *Service) GetConfig(userID int64) (*domain.Config, error) {
	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	if cfg.APIKey != "" {
		if _, err := crypto.Decrypt(cfg.APIKey); err != nil {
			return nil, nil
		}
	}
	_ = cfg.Normalize() // 存量脏值不影响读取展示,补默认即可
	return cfg, nil
}

// GetView 配置视图(未配置时也给出默认地址/条数,前端可直接展示初值)。
func (s *Service) GetView(userID int64) (*ConfigView, error) {
	cfg, err := s.GetConfig(userID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return &ConfigView{
			Enabled:        true,
			Provider:       domain.ProviderDeepSeek,
			BaseURL:        domain.DefaultBaseURL,
			DefaultCount:   domain.DefaultCount,
			TimeoutSeconds: domain.DefaultTimeoutSeconds,
		}, nil
	}

	view := &ConfigView{
		Enabled:        cfg.Enabled,
		Provider:       cfg.Provider,
		BaseURL:        cfg.BaseURL,
		DefaultCount:   cfg.DefaultCount,
		NeedContent:    cfg.NeedContent,
		TimeoutSeconds: cfg.TimeoutSeconds,
	}
	if cfg.APIKey != "" {
		// GetConfig 已验过密文可解,这里失败只可能是并发换钥,按无 Key 处理。
		if plain, err := crypto.Decrypt(cfg.APIKey); err == nil {
			view.HasConfig = true
			view.APIKeyMasked = domain.MaskKey(plain)
		}
	}
	return view, nil
}

// SaveConfig 新建或更新配置(按 user_id 幂等)。
func (s *Service) SaveConfig(userID int64, in SaveInput) (*ConfigView, error) {
	existing, err := s.repo.GetByUserID(userID)
	if err != nil {
		return nil, err
	}

	isNew := existing == nil
	cfg := existing
	if isNew {
		cfg = &domain.Config{UserID: userID, Enabled: true}
	}

	// 逐字段按"未提供即保持"合并:UI 是整表单提交,但地址/条数允许留空。
	if in.Provider != "" {
		cfg.Provider = in.Provider
	}
	if strings.TrimSpace(in.BaseURL) != "" {
		cfg.BaseURL = strings.TrimSpace(in.BaseURL)
	}
	if in.DefaultCount != 0 {
		cfg.DefaultCount = in.DefaultCount
	}
	if in.TimeoutSeconds != 0 {
		cfg.TimeoutSeconds = in.TimeoutSeconds
	}
	if in.NeedContent != nil {
		cfg.NeedContent = *in.NeedContent
	}
	if in.Enabled != nil {
		cfg.Enabled = *in.Enabled
	}

	switch {
	case in.ClearKey:
		cfg.APIKey = ""
	case strings.TrimSpace(in.APIKey) != "":
		key := strings.TrimSpace(in.APIKey)
		if len(key) > 512 {
			return nil, fmt.Errorf("API Key 过长(上限 512 字符)")
		}
		encrypted, err := crypto.Encrypt(key)
		if err != nil {
			return nil, fmt.Errorf("API Key 加密失败")
		}
		cfg.APIKey = encrypted
	}

	if err := cfg.Normalize(); err != nil {
		return nil, err
	}

	now := time.Now()
	cfg.UpdatedAt = now
	if isNew {
		cfg.CreatedAt = now
		if err := s.repo.Create(cfg); err != nil {
			return nil, err
		}
		return s.GetView(userID)
	}
	if err := s.repo.Update(cfg); err != nil {
		return nil, err
	}
	return s.GetView(userID)
}

// ClearConfig 删除配置(回到未配置态)。
func (s *Service) ClearConfig(userID int64) error {
	return s.repo.Delete(userID)
}

// Search 执行一次检索,返回**已格式化**的文本(模型直接可读)。
// 未配置/停用返回 ErrNotConfigured;查询不合法返回 ErrInvalidQuery;三方失败返回 *StatusError。
func (s *Service) Search(ctx context.Context, userID int64, query string, count int) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", ErrInvalidQuery
	}
	if utf8.RuneCountInString(query) > 128 {
		return "", fmt.Errorf("%w:查询词超过 128 字", ErrInvalidQuery)
	}

	cfg, err := s.repo.GetByUserID(userID)
	if err != nil {
		return "", err
	}
	if cfg == nil || !cfg.Enabled || cfg.APIKey == "" {
		return "", ErrNotConfigured
	}
	plainKey, err := crypto.Decrypt(cfg.APIKey)
	if err != nil {
		return "", ErrNotConfigured
	}
	if !cfg.IsUsable() {
		return "", ErrNotConfigured
	}
	_ = cfg.Normalize()

	req := Request{Query: query, Count: cfg.CountOr(count), NeedContent: cfg.NeedContent}
	results, err := s.provider.Search(ctx, cfg, plainKey, req)
	if err != nil {
		return "", err
	}
	return FormatResults(query, results, req.NeedContent), nil
}

// FormatResults 把结果拼成模型可读的纯文本(带预算截断)。
// 空结果返回可行动文案——"没搜到"是正常业务结果,不是错误。
func FormatResults(query string, results []Result, needContent bool) string {
	if len(results) == 0 {
		return fmt.Sprintf("没有搜到与「%s」相关的结果。可以换个说法,或直接用 web_read 读取已知页面。", query)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "搜索「%s」· %d 条结果\n\n", query, len(results))

	for i, r := range results {
		var block strings.Builder
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.URL
		}
		fmt.Fprintf(&block, "%d. %s\n   %s\n", i+1, title, strings.TrimSpace(r.URL))

		summary := strings.TrimSpace(r.Summary)
		if summary == "" {
			summary = strings.TrimSpace(r.Snippet)
		}
		if summary != "" {
			fmt.Fprintf(&block, "   摘要:%s\n", truncateRunes(summary, summaryMaxRunes))
		}
		if needContent {
			if content := strings.TrimSpace(r.Content); content != "" {
				fmt.Fprintf(&block, "   正文:%s\n",
					textutil.TruncateUTF8Safe(content, contentMaxBytes))
			}
		}
		if meta := formatMeta(r); meta != "" {
			fmt.Fprintf(&block, "   来源:%s\n", meta)
		}
		block.WriteString("\n")

		// 总预算兜底:超预算就截断,别让单次搜索吃光上下文
		if b.Len()+block.Len() > resultTotalBudget {
			b.WriteString("...(结果过多已截断,可缩小查询范围后重试)\n")
			break
		}
		b.WriteString(block.String())
	}

	b.WriteString("(需要读某个页面的全文时,用 web_read 打开对应链接)")
	return b.String()
}

// formatMeta 来源行:站点名与发布日期(日期解析失败就原样保留,不丢信息)。
func formatMeta(r Result) string {
	var parts []string
	if site := strings.TrimSpace(r.SiteName); site != "" {
		parts = append(parts, site)
	}
	if date := strings.TrimSpace(r.Date); date != "" {
		if t, err := time.Parse(time.RFC3339, date); err == nil {
			date = t.Format("2006-01-02")
		}
		parts = append(parts, date)
	}
	return strings.Join(parts, " · ")
}

// truncateRunes 按字符截断(中文友好),超长时补省略号。
func truncateRunes(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit]) + "…"
}
