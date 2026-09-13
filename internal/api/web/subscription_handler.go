package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	subdomain "omnibot/internal/domain/subscription"
	"omnibot/internal/middleware"
	"omnibot/pkg/logger"
)

// SubscriptionManager 订阅源管理(14-订阅源管理技术方案,REST 供前端管理页用)。
// 与 agent.SubscriptionManager 同一实现,此处按 handler 需要的窄接口声明。
type SubscriptionManager interface {
	Add(userID int64, siteURL, topicDesc string) (*subdomain.AddResult, error)
	List(userID int64) ([]*subdomain.Subscription, error)
	Remove(userID, id int64) error
	SetStatus(userID, id int64, status string) error
}

// SubscriptionHandler 订阅源管理接口(对话入口之外的页面管理入口):
//   - GET    /api/v1/subscriptions          清单(含暂停,带状态)
//   - POST   /api/v1/subscriptions          订阅 {url, topic_desc?};多候选时返回 candidates
//   - DELETE /api/v1/subscriptions/:id      退订
//   - PUT    /api/v1/subscriptions/:id/status  {status: active|paused}
type SubscriptionHandler struct {
	svc SubscriptionManager
}

func NewSubscriptionHandler(svc SubscriptionManager) *SubscriptionHandler {
	return &SubscriptionHandler{svc: svc}
}

// SubscriptionDTO 订阅条目响应体。
type SubscriptionDTO struct {
	ID        int64  `json:"id"`
	SiteURL   string `json:"site_url"`
	FeedURL   string `json:"feed_url"`
	Title     string `json:"title"`
	TopicDesc string `json:"topic_desc"`
	Status    string `json:"status"`
}

type ListSubscriptionsResponse struct {
	Subscriptions []SubscriptionDTO `json:"subscriptions"`
}

type AddSubscriptionResponse struct {
	Subscription *SubscriptionDTO `json:"subscription,omitempty"` // 入库成功时非空(含幂等重订)
	Candidates   []FeedDTO        `json:"candidates,omitempty"`   // 多候选时非空,等用户挑
}

// FeedDTO 候选 feed(多候选让用户挑时返回)。
type FeedDTO struct {
	FeedURL     string `json:"feed_url"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func toSubscriptionDTO(s *subdomain.Subscription) SubscriptionDTO {
	return SubscriptionDTO{
		ID:        s.ID,
		SiteURL:   s.SiteURL,
		FeedURL:   s.FeedURL,
		Title:     s.Title,
		TopicDesc: s.TopicDesc,
		Status:    s.Status,
	}
}

// HandleListSubscriptions GET /api/v1/subscriptions
func (h *SubscriptionHandler) HandleListSubscriptions(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	subs, err := h.svc.List(userID)
	if err != nil {
		logger.ErrorWithFields("list subscriptions failed", zap.Int64("user_id", userID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "查询订阅失败"})
		return
	}

	items := make([]SubscriptionDTO, 0, len(subs))
	for _, s := range subs {
		items = append(items, toSubscriptionDTO(s))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": ListSubscriptionsResponse{Subscriptions: items}})
}

// HandleAddSubscription POST /api/v1/subscriptions
func (h *SubscriptionHandler) HandleAddSubscription(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	var req struct {
		URL       string `json:"url" binding:"required"`
		TopicDesc string `json:"topic_desc"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "缺少订阅地址"})
		return
	}

	res, err := h.svc.Add(userID, strings.TrimSpace(req.URL), req.TopicDesc)
	if err != nil {
		// 发现失败(「该站点未发现可用的 RSS/Atom 订阅源」等)原样给用户,不硬造
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}
	if len(res.Candidates) > 0 {
		candidates := make([]FeedDTO, 0, len(res.Candidates))
		for _, f := range res.Candidates {
			candidates = append(candidates, FeedDTO{FeedURL: f.FeedURL, Title: f.Title, Description: f.Description})
		}
		c.JSON(http.StatusOK, gin.H{"success": true, "data": AddSubscriptionResponse{Candidates: candidates}})
		return
	}
	sub := res.Subscription
	// 已订阅重复添加时 service 幂等返回既有记录,前端按"返回 subscription 即成功"处理
	c.JSON(http.StatusOK, gin.H{"success": true, "data": AddSubscriptionResponse{Subscription: ptrSubscriptionDTO(sub)}})
}

// HandleDeleteSubscription DELETE /api/v1/subscriptions/:id
func (h *SubscriptionHandler) HandleDeleteSubscription(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的订阅 id"})
		return
	}
	if err := h.svc.Remove(userID, id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "订阅不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// HandleUpdateSubscriptionStatus PUT /api/v1/subscriptions/:id/status
func (h *SubscriptionHandler) HandleUpdateSubscriptionStatus(c *gin.Context) {
	userID := c.GetInt64(middleware.AuthUserIDKey)

	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "无效的订阅 id"})
		return
	}
	var req struct {
		Status string `json:"status" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || (req.Status != subdomain.StatusActive && req.Status != subdomain.StatusPaused) {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "status 仅支持 active/paused"})
		return
	}
	if err := h.svc.SetStatus(userID, id, req.Status); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "订阅不存在"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func ptrSubscriptionDTO(s *subdomain.Subscription) *SubscriptionDTO {
	dto := toSubscriptionDTO(s)
	return &dto
}
