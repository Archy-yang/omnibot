package butler

// scan_service.go — 主动管家每日扫描(26 号)。
// 到点收信号 → LLM 裁决"今天有没有真正值得提的一件事" → 代码层克制闸门 → 落库。
// 红线:宁可漏,不可扰——send:false 是常态与期望输出;克制闸门在代码层,不信 LLM。

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	butlerdomain "omnibot/internal/domain/butler"
	memorydomain "omnibot/internal/domain/memory"
	userdomain "omnibot/internal/domain/user"
	butlerrepo "omnibot/internal/repository/butler"
	"omnibot/pkg/logger"
	"omnibot/pkg/textutil"

	"go.uber.org/zap"
)

// 克制参数(方案 §3~§4/§7:日限硬编码,不给"放开频率"的口子)
const (
	MaxNotesPerDay  = 1                     // 每日上限 1 条(硬编码常量,非配置)
	DedupCooldown   = 7 * 24 * time.Hour    // 同一 dedup_key 7 天冷却
	StaleLoopAfter  = 3 * 24 * time.Hour    // loop open 超过 3 天才算"搁置太久"
	NightGuardStart = 8                     // 22:00-08:00 拒绝触发,与配置无关
	NightGuardEnd   = 22
	MaxSignals      = 20                    // 每类信号最多条数(防巨包)
	MaxSignalChars  = 120                   // 单条信号截断(rune)
)

// ButlerLLM 裁决用 LLM(非流式;装配点按用户解析配置,与 memory.PipelineLLM 同形)。
type ButlerLLM interface {
	Complete(ctx context.Context, userID int64, system, user string) (string, error)
}

// MatterSource 活跃事项信号源(memory.MatterRepository 满足)。
type MatterSource interface {
	ListActiveByUserID(userID int64) ([]*memorydomain.Matter, error)
}

// MemorySource loop/记忆信号源(memory.MemoryRepository 满足)。
type MemorySource interface {
	ListOpenLoops(userID int64, limit int) ([]*memorydomain.Memory, error)
	ListJournal(userID int64, kinds []string, from, to, afterTime *time.Time, afterID int64, limit int) ([]*memorydomain.Memory, error)
}

// UserSettingReader 开关读取(user.UserRepository 满足)。
type UserSettingReader interface {
	GetByID(id int64) (*userdomain.User, error)
}

// Notifier 主动消息送达(飞书卡片;未绑定渠道的实现应静默返回 nil——仅落库即达)。
type Notifier interface {
	NotifyProactive(ctx context.Context, userID int64, content string) error
}

// MessageSink 主动消息落 messages(Kind=proactive,历史可还原)。
type MessageSink interface {
	SaveProactiveMessage(ctx context.Context, userID int64, content string) error
}

// ScanService 主动关心扫描服务。
type ScanService struct {
	notes    butlerrepo.ProactiveNoteRepository
	matters  MatterSource
	memories MemorySource
	users    UserSettingReader
	llm      ButlerLLM // nil = 裁决禁用(每次 no-op)
	notifier Notifier     // 可 nil:仅落库
	sink     MessageSink  // 可 nil:不落消息还原
}

func NewScanService(notes butlerrepo.ProactiveNoteRepository, matters MatterSource,
	memories MemorySource, users UserSettingReader, llm ButlerLLM) *ScanService {
	return &ScanService{notes: notes, matters: matters, memories: memories, users: users, llm: llm}
}

// SetNotifier 注入送达器(装配层在飞书启动后调用)。
func (s *ScanService) SetNotifier(n Notifier) { s.notifier = n }

// SetMessageSink 注入消息还原落库(装配层调用)。
func (s *ScanService) SetMessageSink(m MessageSink) { s.sink = m }

// RunOnce 对单用户执行一次主动关心扫描。返回本次记录(nil=今天没开口,常态)。
// 门槛依次:开关 → 夜间窗 → hour 对位 → 日限 → 空快照 → LLM 裁决 → dedup 冷却。
func (s *ScanService) RunOnce(ctx context.Context, userID int64, now time.Time) (*butlerdomain.ProactiveNote, error) {
	u, err := s.users.GetByID(userID)
	if err != nil {
		return nil, fmt.Errorf("读用户 %d: %w", userID, err)
	}
	if !u.ProactiveEnabled {
		return nil, nil // 红线:默认关闭,显式开启才工作
	}
	if h := now.Hour(); h < NightGuardStart || h >= NightGuardEnd {
		return nil, nil // 夜间硬拒绝,与配置无关
	}
	if u.ProactiveHour != now.Hour() {
		return nil, nil // hour 错位不触发(调度器只是 hourly ticker,对位由这里守)
	}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	sent, err := s.notes.CountDeliveredOnDay(userID, dayStart)
	if err != nil {
		return nil, err
	}
	if sent >= MaxNotesPerDay {
		return nil, nil // 日限已用,不再调 LLM
	}

	snapshot, err := s.buildSnapshot(userID, now)
	if err != nil {
		return nil, err
	}
	if snapshot == "" || s.llm == nil {
		return nil, nil // 无信号/裁决禁用:沉默是常态
	}

	raw, err := s.llm.Complete(ctx, userID, adjudicationSystemPrompt, snapshot)
	if err != nil {
		return nil, fmt.Errorf("主动关心裁决 LLM 调用: %w", err)
	}
	decision := parseDecision(raw)
	if decision == nil {
		return nil, nil // 解析失败/send:false/缺 dedup_key:一律沉默,不猜
	}

	cooled, err := s.notes.HasDeliveredSince(userID, decision.DedupKey, now.Add(-DedupCooldown))
	if err != nil {
		return nil, err
	}

	note := &butlerdomain.ProactiveNote{
		UserID:    userID,
		Kind:      decision.Kind,
		Content:   decision.Content,
		DedupKey:  decision.DedupKey,
		CreatedAt: now,
	}
	if decision.MatterID != nil {
		note.MatterID = decision.MatterID
	} else if id, ok := matterIDFromKey(decision.DedupKey); ok {
		note.MatterID = &id // dedup_key "matter:42" 兜底溯源(LLM 可能省略 matter_id)
	}
	if cooled {
		note.Status = butlerdomain.NoteStatusSkipped // 裁决通过但冷却中:留痕不送
	} else {
		note.Status = butlerdomain.NoteStatusDelivered
		note.DeliveredAt = &now
	}
	if err := s.notes.Create(note); err != nil {
		return nil, err
	}
	if note.Status == butlerdomain.NoteStatusDelivered {
		// 消息还原(Kind=proactive 落 messages):失败仅记日志,note 已持久化不丢
		if s.sink != nil {
			if err := s.sink.SaveProactiveMessage(ctx, userID, note.Content); err != nil {
				logger.ErrorWithFields("butler: save proactive message failed",
					zap.Int64("user_id", userID), zap.Error(err))
			}
		}
		// 送达(飞书卡片/未绑定渠道静默):失败仅记日志,历史可见即"不丢"
		if s.notifier != nil {
			if err := s.notifier.NotifyProactive(ctx, userID, note.Content); err != nil {
				logger.ErrorWithFields("butler: notify proactive failed",
					zap.Int64("user_id", userID), zap.Error(err))
			}
		}
	}
	logger.InfoWithFields("butler: proactive scan decided",
		zap.Int64("user_id", userID), zap.String("status", note.Status),
		zap.String("kind", note.Kind), zap.String("dedup_key", note.DedupKey))
	return note, nil
}

// ---------- 信号收集 ----------

// buildSnapshot 组装三类信号(事项/未决 loop/近 7 天记忆);全空返回 ""。
func (s *ScanService) buildSnapshot(userID int64, now time.Time) (string, error) {
	var b strings.Builder

	matters, err := s.matters.ListActiveByUserID(userID)
	if err != nil {
		return "", fmt.Errorf("收事项信号: %w", err)
	}
	if len(matters) > MaxSignals {
		matters = matters[:MaxSignals]
	}
	if len(matters) > 0 {
		b.WriteString("## 活跃事项\n")
		for _, m := range matters {
			if m.StateDesc != "" {
				fmt.Fprintf(&b, "- [matter:%d] %s:%s\n", m.ID, m.Title, textutil.TruncateRunes(m.StateDesc, MaxSignalChars))
			} else {
				fmt.Fprintf(&b, "- [matter:%d] %s\n", m.ID, m.Title)
			}
		}
		b.WriteString("\n")
	}

	loops, err := s.memories.ListOpenLoops(userID, MaxSignals)
	if err != nil {
		return "", fmt.Errorf("收 loop 信号: %w", err)
	}
	staleCutoff := now.Add(-StaleLoopAfter)
	var stale []*memorydomain.Memory
	for _, l := range loops {
		if l.CreatedAt.Before(staleCutoff) {
			stale = append(stale, l)
		}
	}
	if len(stale) > 0 {
		b.WriteString("## 挂起的待办(open 超过 3 天)\n")
		for _, l := range stale {
			fmt.Fprintf(&b, "- [loop:%d] %s(挂起自 %s)\n",
				l.ID, textutil.TruncateRunes(l.Content, MaxSignalChars), l.CreatedAt.Format("2006-01-02"))
		}
		b.WriteString("\n")
	}

	from := now.AddDate(0, 0, -7)
	mems, err := s.memories.ListJournal(userID, []string{"fact", "loop"}, &from, &now, nil, 0, MaxSignals)
	if err != nil {
		return "", fmt.Errorf("收近期记忆信号: %w", err)
	}
	if len(mems) > 0 {
		b.WriteString("## 近 7 天记忆\n")
		for _, m := range mems {
			fmt.Fprintf(&b, "- [recent:%d] %s\n", m.ID, textutil.TruncateRunes(m.Content, MaxSignalChars))
		}
		b.WriteString("\n")
	}

	return strings.TrimRight(b.String(), "\n"), nil
}

// ---------- LLM 裁决 ----------

const adjudicationSystemPrompt = `你是用户的长期个人管家的"主动关心"裁决器。每天只有一次开口机会,判断今天是否有真正值得主动提的一件事。

红线(按优先级):
1. 宁可漏,不可扰:没有真正值得提的事就保持沉默——send:false 是常态与期望输出。轻微的、可说可不说的,一律不说。
2. 只提一件事:多件候选时只挑最重要、最有时效性的一件。
3. 禁止编造:只能引用输入信号中的事实与日期,不得自行推断未给出的时间或事实。
4. 口吻:贴心的管家,2-3 句以内,自然、具体,给出可行动的建议或一个具体问题;不要客套开场白,不要"您好"。

输出严格 JSON(禁止 markdown 代码块、禁止多余文字):
沉默:{"send": false, "reason": "一句话理由"}
开口:{"send": true, "kind": "matter_due|loop_stale|recent_followup", "dedup_key": "<信号行方括号里给出的主题键,如 matter:42 / loop:101 / recent:201>", "matter_id": <开口对象是事项时给其数字 ID,否则省略>, "content": "..."}`

// decision LLM 裁决输出的结构化形态。
type decision struct {
	Send     bool   `json:"send"`
	Kind     string `json:"kind"`
	DedupKey string `json:"dedup_key"`
	Content  string `json:"content"`
	MatterID *int64 `json:"matter_id"`
}

// matterIDFromKey 从 dedup_key "matter:42" 解析事项 ID。
func matterIDFromKey(key string) (int64, bool) {
	idStr, ok := strings.CutPrefix(key, "matter:")
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// parseDecision 容错解析裁决输出:去代码围栏 → 提取首尾大括号 → JSON 解析。
// 任何不满足"send:true 且有 dedup_key 与 content"的输出都返回 nil(沉默,不猜)。
func parseDecision(raw string) *decision {
	s := strings.TrimSpace(raw)
	// 剥 ```json ... ``` 围栏
	if i := strings.Index(s, "{"); i >= 0 {
		if j := strings.LastIndex(s, "}"); j > i {
			s = s[i : j+1]
		}
	}
	var d decision
	if err := json.Unmarshal([]byte(s), &d); err != nil {
		return nil
	}
	if !d.Send || d.DedupKey == "" || strings.TrimSpace(d.Content) == "" {
		return nil
	}
	if d.Kind == "" {
		d.Kind = butlerdomain.NoteKindRecentFollowup
	}
	return &d
}
