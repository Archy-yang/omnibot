package subscription

import (
	"fmt"
	"strings"

	subscriptiondomain "omnibot/internal/domain/subscription"
	subscriptionrepo "omnibot/internal/repository/subscription"
)

// SubscriptionService 订阅源登记簿服务(14-订阅源管理技术方案 §3/§5)。
// 层级约束:Agent 工具 → 本服务 → Repository;发现(Discoverer)为确定性代码。
type SubscriptionService interface {
	// Add 订阅。用户给网站或 feed 地址均可:
	//   - 地址本身就是可用 feed → 直接用(Validate 捷径)
	//   - 否则走自动发现;恰 1 个候选 → 入库;多个 → 返回 Candidates 让用户挑
	//   - 同 (user, feed_url) 已订阅 → 幂等返回既有记录,不报错
	// topicDesc 用户补充的描述,缺省由 feed 元数据组合。
	Add(userID int64, siteURL, topicDesc string) (*subscriptiondomain.AddResult, error)
	// List 全量(含 paused,由工具层标注状态;选源时 paused 跳过)。
	List(userID int64) ([]*subscriptiondomain.Subscription, error)
	Remove(userID, id int64) error
	SetStatus(userID, id int64, status string) error
}

type subscriptionService struct {
	repo subscriptionrepo.SubscriptionRepository
	disc Discoverer
}

func NewSubscriptionService(repo subscriptionrepo.SubscriptionRepository, disc Discoverer) SubscriptionService {
	return &subscriptionService{repo: repo, disc: disc}
}

func (s *subscriptionService) Add(userID int64, siteURL, topicDesc string) (*subscriptiondomain.AddResult, error) {
	siteURL = strings.TrimSpace(siteURL)
	if siteURL == "" {
		return nil, fmt.Errorf("订阅地址不能为空")
	}

	// 捷径:给的地址本身就是可用 feed
	var info *subscriptiondomain.FeedInfo
	if direct, err := s.disc.Validate(siteURL); err == nil {
		info = direct
	} else {
		feeds, err := s.disc.Discover(siteURL)
		if err != nil {
			return nil, err // 「该站点未发现可用的 RSS/Atom 订阅源」如实上抛
		}
		if len(feeds) > 1 {
			return &subscriptiondomain.AddResult{Candidates: feeds}, nil
		}
		info = feeds[0]
	}

	// 幂等:同 (user, feed_url) 已订阅 → 返回既有记录
	if existing, err := s.repo.GetByUserAndFeed(userID, info.FeedURL); err == nil && existing != nil {
		return &subscriptiondomain.AddResult{Subscription: existing}, nil
	}

	sub := &subscriptiondomain.Subscription{
		UserID:     userID,
		SiteURL:    siteURL,
		FeedURL:    info.FeedURL,
		Title:      orDefault(info.Title, "未命名订阅源"),
		TopicDesc:  composeTopicDesc(topicDesc, info),
		Status:     subscriptiondomain.StatusActive,
		CreatedVia: subscriptiondomain.ViaManual,
	}
	if err := s.repo.Create(sub); err != nil {
		return nil, err
	}
	return &subscriptiondomain.AddResult{Subscription: sub}, nil
}

func (s *subscriptionService) List(userID int64) ([]*subscriptiondomain.Subscription, error) {
	return s.repo.ListByUserID(userID, true)
}

func (s *subscriptionService) Remove(userID, id int64) error {
	return s.repo.Delete(userID, id)
}

func (s *subscriptionService) SetStatus(userID, id int64, status string) error {
	if status != subscriptiondomain.StatusActive && status != subscriptiondomain.StatusPaused {
		return fmt.Errorf("非法状态: %s", status)
	}
	return s.repo.UpdateStatus(userID, id, status)
}

// composeTopicDesc 选源依据:用户补充优先,缺省由 feed 元数据组合「标题:描述」。
func composeTopicDesc(userDesc string, info *subscriptiondomain.FeedInfo) string {
	if d := strings.TrimSpace(userDesc); d != "" {
		return d
	}
	title := strings.TrimSpace(info.Title)
	desc := strings.TrimSpace(info.Description)
	switch {
	case title != "" && desc != "":
		return truncateRunes(title+"："+desc, 200)
	case title != "":
		return truncateRunes(title, 200)
	case desc != "":
		return truncateRunes(desc, 200)
	default:
		return ""
	}
}

// truncateRunes 按 rune 截断,避免中文描述超列宽。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}
