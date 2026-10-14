package memory

import "time"

// 风格与认知画像服务(24号 §5):证据只读展示 + 单条删除(纠正口)。
// 注入通道属 M2(per-user prompt 装配),本文件不含任何注入逻辑。

// StyleObservationView 画像观察条目(带溯源片段)。
type StyleObservationView struct {
	ID        int64         `json:"id"`
	Dimension string        `json:"dimension"`
	Content   string        `json:"content"`
	CreatedAt time.Time     `json:"created_at"`
	Links     []JournalLink `json:"links"`
}

// CognitionChangeView 认知变化条目。
type CognitionChangeView struct {
	ID        int64         `json:"id"`
	Content   string        `json:"content"`
	CreatedAt time.Time     `json:"created_at"`
	Links     []JournalLink `json:"links"`
}

// StyleProfileView 画像视图。
type StyleProfileView struct {
	Observations []StyleObservationView `json:"observations"`
	Changes      []CognitionChangeView  `json:"changes"`
}

// linkBriefs 批量取消息摘要(时间升序拼装;消息已消失静默缺失)。
func (s *memoryService) linkBriefs(msgIDs []int64) map[int64]JournalLink {
	briefs := map[int64]JournalLink{}
	if s.msgSource == nil || len(msgIDs) == 0 {
		return briefs
	}
	seen := make(map[int64]bool, len(msgIDs))
	uniq := make([]int64, 0, len(msgIDs))
	for _, id := range msgIDs {
		if id > 0 && !seen[id] {
			seen[id] = true
			uniq = append(uniq, id)
		}
	}
	if len(uniq) == 0 {
		return briefs
	}
	msgs, err := s.msgSource.GetByIDs(uniq)
	if err != nil {
		return briefs // 溯源降级为空,不阻断画像
	}
	for _, m := range msgs {
		briefs[m.ID] = JournalLink{MessageID: m.ID, OccurredAt: m.CreatedAt, Snippet: snippet(m.Content)}
	}
	return briefs
}

// sortLinksByTime 按消息时间升序(就地插入排序;单条目链接数小)。
func sortLinksByTime(ls []JournalLink) {
	for i := 1; i < len(ls); i++ {
		for j := i; j > 0 && ls[j].OccurredAt.Before(ls[j-1].OccurredAt); j-- {
			ls[j], ls[j-1] = ls[j-1], ls[j]
		}
	}
}

// StyleProfile 画像视图:观察 + 认知变化,各限 limit 条(默认/上限 50),按时间倒序。
func (s *memoryService) StyleProfile(userID int64, limit int) (*StyleProfileView, error) {
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	obs, err := s.repo.ListStyleObservations(userID, limit)
	if err != nil {
		return nil, err
	}
	changes, err := s.repo.ListCognitionChanges(userID, limit)
	if err != nil {
		return nil, err
	}
	// 溯源一次性回表
	allIDs := make([]int64, 0, len(obs)+len(changes))
	for _, o := range obs {
		allIDs = append(allIDs, o.SourceMessageIDs...)
	}
	for _, c := range changes {
		allIDs = append(allIDs, c.SourceMessageIDs...)
	}
	briefs := s.linkBriefs(allIDs)

	view := &StyleProfileView{
		Observations: make([]StyleObservationView, 0, len(obs)),
		Changes:      make([]CognitionChangeView, 0, len(changes)),
	}
	for _, o := range obs {
		ls := make([]JournalLink, 0, len(o.SourceMessageIDs))
		for _, id := range o.SourceMessageIDs {
			if b, ok := briefs[id]; ok {
				ls = append(ls, b)
			}
		}
		sortLinksByTime(ls)
		view.Observations = append(view.Observations, StyleObservationView{
			ID: o.ID, Dimension: o.Dimension, Content: o.Content, CreatedAt: o.CreatedAt, Links: ls,
		})
	}
	for _, c := range changes {
		ls := make([]JournalLink, 0, len(c.SourceMessageIDs))
		for _, id := range c.SourceMessageIDs {
			if b, ok := briefs[id]; ok {
				ls = append(ls, b)
			}
		}
		sortLinksByTime(ls)
		view.Changes = append(view.Changes, CognitionChangeView{
			ID: c.ID, Content: c.Content, CreatedAt: c.CreatedAt, Links: ls,
		})
	}
	return view, nil
}

// DeleteStyleObservation 删除单条观察(纠正口;他人 → 未命中 false)。
func (s *memoryService) DeleteStyleObservation(userID, id int64) (bool, error) {
	return s.repo.DeleteStyleObservation(userID, id)
}

// DeleteCognitionChange 删除单条认知变化。
func (s *memoryService) DeleteCognitionChange(userID, id int64) (bool, error) {
	return s.repo.DeleteCognitionChange(userID, id)
}
