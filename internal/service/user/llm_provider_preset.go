package user

const (
	ProviderModeOpenAICompatible = "openai_compatible"
	ProviderModeNative           = "native"

	ProviderStatusAvailable = "available"
	ProviderStatusDisabled  = "disabled"
)

const nativeProviderDisabledReason = "专用接口暂不可用，请使用 OpenAI 兼容模式。"

// ProviderOption 服务商预设选项
type ProviderOption struct {
	Value          string
	Label          string
	Mode           string
	Status         string
	DefaultBaseURL string
	DefaultModel   string
	Description    string
	DisabledReason string
}

var providerOptions = []ProviderOption{
	{
		Value:          "openai",
		Label:          "OpenAI 官方",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://api.openai.com/v1",
		DefaultModel:   "gpt-4o-mini",
		Description:    "OpenAI 官方 Chat Completions API。",
	},
	{
		Value:          "baidu_qianfan",
		Label:          "百度千帆",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://qianfan.baidubce.com/v2",
		DefaultModel:   "ernie-4.0-turbo-8k",
		Description:    "百度智能云千帆 OpenAI 兼容模式。",
	},
	{
		Value:          "volcengine",
		Label:          "字节火山",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://ark.cn-beijing.volces.com/api/v3",
		DefaultModel:   "doubao-seed-1-6",
		Description:    "火山方舟 OpenAI 兼容模式。",
	},
	{
		Value:          "aliyun_qwen",
		Label:          "阿里千问",
		Mode:           ProviderModeOpenAICompatible,
		Status:         ProviderStatusAvailable,
		DefaultBaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
		DefaultModel:   "qwen-plus",
		Description:    "阿里云 DashScope OpenAI 兼容模式。",
	},
	{
		Value:       "custom_openai_compatible",
		Label:       "自定义 OpenAI-compatible",
		Mode:        ProviderModeOpenAICompatible,
		Status:      ProviderStatusAvailable,
		Description: "适用于 One API、New API、vLLM、Ollama 网关或其他 OpenAI 兼容服务。",
	},
	{
		Value:          "qianfan_native",
		Label:          "百度千帆专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "百度千帆原生接口。",
		DisabledReason: nativeProviderDisabledReason,
	},
	{
		Value:          "qwen_native",
		Label:          "通义千问专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "通义千问 DashScope 原生接口。当前请使用 OpenAI 兼容 → 阿里千问。",
		DisabledReason: nativeProviderDisabledReason,
	},
	{
		Value:          "doubao_native",
		Label:          "豆包专用",
		Mode:           ProviderModeNative,
		Status:         ProviderStatusDisabled,
		Description:    "火山方舟豆包原生接口。当前请使用 OpenAI 兼容 → 字节火山。",
		DisabledReason: nativeProviderDisabledReason,
	},
}

func listProviderOptions() []ProviderOption {
	items := make([]ProviderOption, len(providerOptions))
	copy(items, providerOptions)
	return items
}

func findProviderOption(value string) (ProviderOption, bool) {
	for _, item := range providerOptions {
		if item.Value == value {
			return item, true
		}
	}
	return ProviderOption{}, false
}

func isLegacyProvider(value string) bool {
	switch value {
	case "qwen", "doubao", "anthropic", "azure":
		return true
	default:
		return false
	}
}
