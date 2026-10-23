package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
)

// Journal/Archive 服务(21 号 §5/§6):把记忆底座变成用户可感知可纠正的个人经历层。
// Journal = memories 的时间轴视图(有效时间=COALESCE(happened_at,created_at));
// Archive = Matter 档案视图(列表 + 演进时间线 + 用户纠正口)。

var (
	ErrJournalInvalidKind   = errors.New("journal: kind 取值非法")
	ErrJournalInvalidCursor = errors.New("journal: 游标非法")
	ErrJournalInvalidStatus = errors.New("journal: status 取值非法")
	ErrMatterNotFound       = errors.New("journal: 档案不存在")
	ErrMatterNoFields       = errors.New("journal: 未指定任何修改字段")
	ErrMatterDescTooLong    = errors.New("journal: 状态描述超长")
)

const (
	journalDefaultLimit     = 50
	journalMaxLimit         = 200
	snippetMaxRunes         = 80
	matterStateDescMaxRunes = 2000
)

// JournalLink 溯源消息的展示摘要(片段 + 消息时间)。
type JournalLink struct {
	MessageID  int64     `json:"message_id"`
	OccurredAt time.Time `json:"occurred_at"`
	Snippet    string    `json:"snippet"`
}

// JournalItem 时间轴条目。
type JournalItem struct {
	ID         int64         `json:"id"`
	Content    string        `json:"content"`
	Kind       string        `json:"kind"`
	Source     string        `json:"source"`
	Pinned     bool          `json:"pinned"`
	HappenedAt *time.Time    `json:"happened_at,omitempty"` // NULL 时前端回落 created_at(标注"沉淀于")
	CreatedAt  time.Time     `json:"created_at"`
	MatterID   *int64        `json:"matter_id,omitempty"`
	Links      []JournalLink `json:"links"`
}

// MatterSummary 档案列表项。
type MatterSummary struct {
	ID          int64      `json:"id"`
	Title       string     `json:"title"`
	StateDesc   string     `json:"state_desc"`
	Status      string     `json:"status"`
	UpdatedAt   time.Time  `json:"updated_at"`
	MemoryCount int64      `json:"memory_count"`
	DocURL      string     `json:"doc_url,omitempty"` // 23号:已同步的飞书文档
	SyncedAt    *time.Time `json:"synced_at,omitempty"`
	TargetAt    *time.Time `json:"target_at,omitempty"` // 28号 N2:目标日(非空 = 目标事项)
}

// MatterDetail 档案详情:元信息 + 演进时间线。
type MatterDetail struct {
	ID          int64         `json:"id"`
	Title       string        `json:"title"`
	StateDesc   string        `json:"state_desc"`
	Status      string        `json:"status"`
	UpdatedAt   time.Time     `json:"updated_at"`
	MemoryCount int64         `json:"memory_count"`
	DocURL      string        `json:"doc_url,omitempty"` // 23号
	SyncedAt    *time.Time    `json:"synced_at,omitempty"`
	TargetAt    *time.Time    `json:"target_at,omitempty"` // 28号 N3:目标日回显
	Timeline    []JournalItem `json:"timeline"`
}

// journalCursor 游标载体(不透明字符串 = base64(json))。
type journalCursor struct {
	Time time.Time `json:"t"`
	ID   int64     `json:"i"`
}

func encodeJournalCursor(c journalCursor) string {
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func decodeJournalCursor(s string) (journalCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return journalCursor{}, ErrJournalInvalidCursor
	}
	var c journalCursor
	if err := json.Unmarshal(raw, &c); err != nil {
		return journalCursor{}, ErrJournalInvalidCursor
	}
	return c, nil
}

// journalKinds kind 参数 → 仓储过滤集("" 与 all 同义)。
func journalKinds(kind string) ([]string, error) {
	switch kind {
	case "", "all":
		return []string{memorydomain.MemoryKindFact, memorydomain.MemoryKindLoop}, nil
	case memorydomain.MemoryKindFact, memorydomain.MemoryKindLoop:
		return []string{kind}, nil
	default:
		return nil, ErrJournalInvalidKind
	}
}

// Journal 时间轴:分页查询 + 溯源拼装。返回 nextCursor(空=没有更多)。
func (s *memoryService) Journal(ctx context.Context, userID int64, kind string, from, to *time.Time, cursor string, limit int) ([]JournalItem, string, error) {
	kinds, err := journalKinds(kind)
	if err != nil {
		return nil, "", err
	}
	if limit <= 0 || limit > journalMaxLimit {
		limit = journalDefaultLimit
	}
	var afterTime *time.Time
	var afterID int64
	if cursor != "" {
		c, err := decodeJournalCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		afterTime, afterID = &c.Time, c.ID
	}
	rows, err := s.repo.ListJournal(userID, kinds, from, to, afterTime, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	items, err := s.assembleItems(ctx, rows)
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(rows) == limit {
		last := rows[len(rows)-1]
		next = encodeJournalCursor(journalCursor{Time: effectiveTime(last), ID: last.ID})
	}
	return items, next, nil
}

// effectiveTime 条目的有效时间(时间轴锚):事件时间优先,回落沉淀时间。
func effectiveTime(m *memorydomain.Memory) time.Time {
	if m.HappenedAt != nil {
		return *m.HappenedAt
	}
	return m.CreatedAt
}

// assembleItems 溯源拼装:links → 消息摘要(按消息时间升序);消息已消失静默缺失。
func (s *memoryService) assembleItems(ctx context.Context, rows []*memorydomain.Memory) ([]JournalItem, error) {
	if len(rows) == 0 {
		return []JournalItem{}, nil
	}
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	links, err := s.repo.ListLinksForMemories(ids)
	if err != nil {
		return nil, err
	}
	// 消息摘要回表
	msgIDs := make([]int64, 0, len(links))
	seen := make(map[int64]bool, len(links))
	for _, l := range links {
		if !seen[l.MessageID] {
			seen[l.MessageID] = true
			msgIDs = append(msgIDs, l.MessageID)
		}
	}
	briefs := map[int64]JournalLink{}
	if s.msgSource != nil && len(msgIDs) > 0 {
		msgs, err := s.msgSource.GetByIDs(msgIDs)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			briefs[m.ID] = JournalLink{MessageID: m.ID, OccurredAt: m.CreatedAt, Snippet: snippet(m.Content)}
		}
	}
	byMemory := map[int64][]JournalLink{}
	for _, l := range links {
		if b, ok := briefs[l.MessageID]; ok {
			byMemory[l.MemoryID] = append(byMemory[l.MemoryID], b)
		}
	}
	// 每条 link 组按消息时间升序
	items := make([]JournalItem, 0, len(rows))
	for _, r := range rows {
		ls := byMemory[r.ID]
		if len(ls) > 1 {
			for i := 1; i < len(ls); i++ {
				for j := i; j > 0 && ls[j].OccurredAt.Before(ls[j-1].OccurredAt); j-- {
					ls[j], ls[j-1] = ls[j-1], ls[j]
				}
			}
		}
		items = append(items, JournalItem{
			ID: r.ID, Content: r.Content, Kind: r.Kind, Source: r.Source, Pinned: r.Pinned,
			HappenedAt: r.HappenedAt, CreatedAt: r.CreatedAt, MatterID: r.MatterID, Links: ls,
		})
	}
	return items, nil
}

func snippet(content string) string {
	r := []rune(content)
	if len(r) > snippetMaxRunes {
		return string(r[:snippetMaxRunes])
	}
	return content
}

// Matters 档案列表(status 空=全部,active/done/archived 可选)。
func (s *memoryService) Matters(userID int64, status string) ([]MatterSummary, error) {
	switch status {
	case "", memorydomain.MatterStatusActive, memorydomain.MatterStatusDone, memorydomain.MatterStatusArchived:
	default:
		return nil, ErrJournalInvalidStatus
	}
	matters, err := s.matterRepo.ListByUserID(userID, status)
	if err != nil {
		return nil, err
	}
	counts, err := s.repo.CountByMatter(userID)
	if err != nil {
		return nil, err
	}
	out := make([]MatterSummary, 0, len(matters))
	for _, m := range matters {
		out = append(out, MatterSummary{
			ID: m.ID, Title: m.Title, StateDesc: m.StateDesc, Status: m.Status,
			UpdatedAt: m.UpdatedAt, MemoryCount: counts[m.ID],
			DocURL: m.FeishuDocURL, SyncedAt: m.SyncedAt, TargetAt: m.TargetAt,
		})
	}
	return out, nil
}

// MatterDetail 档案详情:元信息 + 演进时间线(pinned 前置,有效时间升序)。
func (s *memoryService) MatterDetail(userID int64, id int64) (*MatterDetail, error) {
	m, err := s.matterRepo.GetByID(userID, id)
	if err != nil {
		return nil, err
	}
	if m == nil {
		return nil, ErrMatterNotFound
	}
	rows, err := s.repo.ListByMatter(userID, id)
	if err != nil {
		return nil, err
	}
	timeline, err := s.assembleItems(context.Background(), rows)
	if err != nil {
		return nil, err
	}
	return &MatterDetail{
		ID: m.ID, Title: m.Title, StateDesc: m.StateDesc, Status: m.Status,
		UpdatedAt: m.UpdatedAt, MemoryCount: int64(len(rows)), Timeline: timeline,
		DocURL: m.FeishuDocURL, SyncedAt: m.SyncedAt, TargetAt: m.TargetAt,
	}, nil
}

// UpdateMatter 用户纠正口:只允许 StateDesc/Status(21 号 §1.1:title 不可改)。
// 返回是否命中(他人/不存在 → ErrMatterNotFound)。
func (s *memoryService) UpdateMatter(userID, id int64, stateDesc, status *string) (bool, error) {
	if stateDesc == nil && status == nil {
		return false, ErrMatterNoFields
	}
	if status != nil {
		switch *status {
		case memorydomain.MatterStatusActive, memorydomain.MatterStatusDone, memorydomain.MatterStatusArchived:
		default:
			return false, ErrJournalInvalidStatus
		}
	}
	if stateDesc != nil && utf8.RuneCountInString(*stateDesc) > matterStateDescMaxRunes {
		return false, ErrMatterDescTooLong
	}
	ok, err := s.matterRepo.UpdateFields(userID, id, stateDesc, status)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, ErrMatterNotFound // 他人/不存在:一律按未找到,不区分泄露存在性
	}
	return true, nil
}

// MatterIDByTitle 按标题解析档案 ID(22号:search_memories 的 matter_title 参数)。
// 不存在 → (0,false);service 层不报错——调用方(LLM)需要的是"换个说法"而不是异常。
func (s *memoryService) MatterIDByTitle(userID int64, title string) (int64, bool) {
	m, err := s.matterRepo.GetByTitle(userID, title)
	if err != nil || m == nil {
		return 0, false
	}
	return m.ID, true
}
