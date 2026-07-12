package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"omnibot/internal/middleware"
	userLLM "omnibot/internal/service/user"
)

// BindingService 飞书绑定服务接口(web handler 依赖抽象)
type BindingService interface {
	GenerateCode(userID int64) (code string, expiresAt time.Time, err error)
	IsFeishuBound(userID int64) (bool, error)
	// BindFeishu / ResolveFeishuUserID 飞书端用,web handler 不调用,但接口对齐 service 实现
	BindFeishu(code, openID string) error
	ResolveFeishuUserID(openID string) (userID int64, bound bool, err error)
}

// FeishuBindHandler web 端飞书绑定接口(状态查询 + 出码)
type FeishuBindHandler struct {
	svc BindingService
}

// NewFeishuBindHandler 创建飞书绑定处理器
func NewFeishuBindHandler(svc BindingService) *FeishuBindHandler {
	return &FeishuBindHandler{svc: svc}
}

// bindingStatusResponse 绑定状态响应
type bindingStatusResponse struct {
	Bound bool `json:"bound"`
}

// bindCodeResponse 绑定码响应
type bindCodeResponse struct {
	Code       string `json:"code"`
	ExpiresAt  string `json:"expires_at"` // RFC3339
	ExpiresIn  int    `json:"expires_in"` // 剩余秒数,前端倒计时用
}

// HandleGetBindingStatus GET /api/v1/user/feishu/binding
// 返回当前账号是否已绑定飞书。
func (h *FeishuBindHandler) HandleGetBindingStatus(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	bound, err := h.svc.IsFeishuBound(userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "查询绑定状态失败"})
		return
	}
	c.JSON(http.StatusOK, bindingStatusResponse{Bound: bound})
}

// HandleGenerateBindCode POST /api/v1/user/feishu/bind-code
// 生成 6 位绑定码(5 分钟有效)。账号已绑飞书 -> 409。
func (h *FeishuBindHandler) HandleGenerateBindCode(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)
	code, expires, err := h.svc.GenerateCode(userID)
	if err != nil {
		if errors.Is(err, userLLM.ErrAccountAlreadyBound) {
			c.JSON(http.StatusConflict, gin.H{"error": "你的账号已绑定飞书"})
			return
		}
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
