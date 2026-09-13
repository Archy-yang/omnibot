package web

// llm_config_handler.go — 用户 LLM 配置接口(自 handler.go 按资源拆出,方法仍在 Handler 上)。
// 路由:/api/v1/user/llm-providers、/api/v1/user/llm-config(见 routes.go)。

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"omnibot/internal/middleware"
	userLLM "omnibot/internal/service/user"
)

// ========== LLM 配置接口 ==========

// LLMProviderOptionDTO 服务商预设选项 DTO
type LLMProviderOptionDTO struct {
	Value          string `json:"value"`
	Label          string `json:"label"`
	Mode           string `json:"mode"`
	Status         string `json:"status"`
	DefaultBaseURL string `json:"default_base_url,omitempty"`
	DefaultModel   string `json:"default_model,omitempty"`
	Description    string `json:"description,omitempty"`
	DisabledReason string `json:"disabled_reason,omitempty"`
}

// GetLLMProvidersResponse 提供商列表响应
type GetLLMProvidersResponse struct {
	Providers []LLMProviderOptionDTO `json:"providers"`
}

func toProviderOptionDTO(opt userLLM.ProviderOption) LLMProviderOptionDTO {
	return LLMProviderOptionDTO{
		Value:          opt.Value,
		Label:          opt.Label,
		Mode:           opt.Mode,
		Status:         opt.Status,
		DefaultBaseURL: opt.DefaultBaseURL,
		DefaultModel:   opt.DefaultModel,
		Description:    opt.Description,
		DisabledReason: opt.DisabledReason,
	}
}

// HandleGetLLMProviders 获取所有可用的 LLM 提供商选项
func (h *Handler) HandleGetLLMProviders(c *gin.Context) {
	options := h.llmConfigService.ListProviderOptions()

	items := make([]LLMProviderOptionDTO, len(options))
	for i, opt := range options {
		items[i] = toProviderOptionDTO(opt)
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": GetLLMProvidersResponse{
			Providers: items,
		},
	})
}

// GetLLMConfigResponse 获取配置响应
type GetLLMConfigResponse struct {
	HasConfig   bool    `json:"has_config"`
	APIKeyMask  string  `json:"api_key_masked"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model"`
	Provider    string  `json:"provider"`
	StatusText  string  `json:"status_text"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	// 用户级向量配置回显(12-记忆系统技术方案 §5.3):未配置为空;Key 脱敏
	EmbeddingProvider     string `json:"embedding_provider"`
	EmbeddingBaseURL      string `json:"embedding_base_url"`
	EmbeddingModel        string `json:"embedding_model"`
	EmbeddingDims         int    `json:"embedding_dims"`
	EmbeddingAPIKeyMasked string `json:"embedding_api_key_masked"`
	HasEmbeddingConfig    bool   `json:"has_embedding_config"`
}

// HandleGetLLMConfig 获取用户 LLM 配置
func (h *Handler) HandleGetLLMConfig(c *gin.Context) {
	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	configView, err := h.llmConfigService.GetConfigView(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "获取配置失败",
		})
		return
	}

	resp := GetLLMConfigResponse{
		HasConfig:   configView.HasConfig,
		APIKeyMask:  configView.APIKeyMasked,
		BaseURL:     configView.BaseURL,
		Model:       configView.Model,
		Provider:    configView.Provider,
		StatusText:  configView.StatusText,
		Temperature: configView.Temperature,
		MaxTokens:   configView.MaxTokens,
		// 用户级向量配置回显(§5.3)
		EmbeddingProvider:     configView.EmbeddingProvider,
		EmbeddingBaseURL:      configView.EmbeddingBaseURL,
		EmbeddingModel:        configView.EmbeddingModel,
		EmbeddingDims:         configView.EmbeddingDims,
		EmbeddingAPIKeyMasked: configView.EmbeddingAPIKeyMasked,
		HasEmbeddingConfig:    configView.HasEmbeddingConfig,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    resp,
	})
}

// UpdateLLMConfigRequest 更新配置请求
type UpdateLLMConfigRequest struct {
	Provider    string  `json:"provider" binding:"required"`
	APIKey      string  `json:"api_key"`
	BaseURL     string  `json:"base_url"`
	Model       string  `json:"model" binding:"required"`
	Temperature float64 `json:"temperature"`
	MaxTokens   int     `json:"max_tokens"`
	// DisableThinking 快模式(M5/C):跳过模型思考阶段换低延迟
	DisableThinking bool `json:"disable_thinking"`
	// 用户级向量配置(12-记忆系统技术方案 §5.3):全空=不设置,部分填写=服务端校验拒绝
	EmbeddingProvider string `json:"embedding_provider"`
	EmbeddingBaseURL  string `json:"embedding_base_url"`
	EmbeddingAPIKey   string `json:"embedding_api_key"`
	EmbeddingModel    string `json:"embedding_model"`
	EmbeddingDims     int    `json:"embedding_dims"`
	// 显式清除用户级向量配置(前端选"使用系统默认"并保存时发送)
	ClearEmbedding bool `json:"clear_embedding"`
}

// HandleUpdateLLMConfig 更新用户 LLM 配置
func (h *Handler) HandleUpdateLLMConfig(c *gin.Context) {
	var req UpdateLLMConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "请求参数错误: " + err.Error(),
		})
		return
	}

	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	updateReq := userLLM.UpdateConfigRequest{
		Provider:        req.Provider,
		APIKey:          req.APIKey,
		BaseURL:         req.BaseURL,
		Model:           req.Model,
		Temperature:     req.Temperature,
		MaxTokens:       req.MaxTokens,
		DisableThinking: req.DisableThinking,
		// 用户级向量配置(§5.3)
		EmbeddingProvider: req.EmbeddingProvider,
		EmbeddingBaseURL:  req.EmbeddingBaseURL,
		EmbeddingAPIKey:   req.EmbeddingAPIKey,
		EmbeddingModel:    req.EmbeddingModel,
		EmbeddingDims:     req.EmbeddingDims,
		ClearEmbedding:    req.ClearEmbedding,
	}

	if err := h.llmConfigService.UpdateFullConfig(userID, updateReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置保存成功",
	})
}

// HandleDeleteLLMConfig 删除用户 LLM 配置
func (h *Handler) HandleDeleteLLMConfig(c *gin.Context) {
	// v2.1: 身份由 AuthRequired 中间件注入
	userID := c.GetInt64(middleware.AuthUserIDKey)

	if err := h.llmConfigService.ClearConfig(userID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   "清除配置失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "配置已清除",
	})
}
