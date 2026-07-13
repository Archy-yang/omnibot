package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"omnibot/internal/middleware"
)

// BindingService 账号绑定服务接口(web handler 依赖抽象,v2.3 泛化渠道通用)
type BindingService interface {
	GenerateCode(userID int64) (code string, expiresAt time.Time, err error)
	IsChannelBound(userID int64, channelType string) (bool, error)
	BindChannel(channelType, code, openID string) error
	ResolveUserID(channelType, openID string) (userID int64, bound bool, err error)
}

// 支持绑定的渠道(v2.3:飞书 + 微信)
var supportedChannels = []string{"feishu", "wechat"}

// ChannelBindHandler web 端渠道绑定接口(状态查询 + 出码)
type ChannelBindHandler struct {
	svc BindingService
}

// NewChannelBindHandler 创建渠道绑定处理器
func NewChannelBindHandler(svc BindingService) *ChannelBindHandler {
	return &ChannelBindHandler{svc: svc}
}

// bindingStatusResponse 绑定状态响应(返各渠道是否已绑)
type bindingStatusResponse struct {
	FeishuBound bool `json:"feishu_bound"`
	WeChatBound bool `json:"wechat_bound"`
}

// bindCodeResponse 绑定码响应
type bindCodeResponse struct {
	Code      string `json:"code"`
	ExpiresAt string `json:"expires_at"` // RFC3339
	ExpiresIn int    `json:"expires_in"` // 剩余秒数,前端倒计时用
}

// HandleGetBindingStatus GET /api/v1/user/channel-binding
// 返回当前账号各渠道的绑定状态。
func (h *ChannelBindHandler) HandleGetBindingStatus(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	resp := bindingStatusResponse{}
	for _, ch := range supportedChannels {
		bound, err := h.svc.IsChannelBound(userID, ch)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定状态失败"})
			return
		}
		switch ch {
		case "feishu":
			resp.FeishuBound = bound
		case "wechat":
			resp.WeChatBound = bound
		}
	}
	c.JSON(http.StatusOK, resp)
}

// HandleGenerateBindCode POST /api/v1/user/channel-binding/bind-code
// 生成 6 位绑定码(5 分钟有效,通用码不区分渠道)。
// 若账号已绑定全部支持渠道 -> 409(没有可绑的渠道了)。
func (h *ChannelBindHandler) HandleGenerateBindCode(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	// 全渠道都已绑 -> 无需出码
	allBound := true
	for _, ch := range supportedChannels {
		bound, err := h.svc.IsChannelBound(userID, ch)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "生成绑定码失败"})
			return
		}
		if !bound {
			allBound = false
			break
		}
	}
	if allBound {
		c.JSON(http.StatusConflict, gin.H{"error": "你的账号已绑定全部渠道"})
		return
	}

	code, expires, err := h.svc.GenerateCode(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "生成绑定码失败"})
		return
	}
	expiresIn := int(time.Until(expires).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	c.JSON(http.StatusOK, bindCodeResponse{
		Code:      code,
		ExpiresAt: expires.Format(time.RFC3339),
		ExpiresIn: expiresIn,
	})
}
