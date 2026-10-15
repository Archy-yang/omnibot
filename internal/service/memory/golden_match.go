package memory

// golden_match.go — 金样本加载与匹配(24 号 M3)。
// E1 确定性测试与 E2 真实回放(make eval-memory)共用同一套匹配语义,
// 保证"测试绿的"与"报告说命中"是同一回事。
//
// 断言语义(doc 24 §7.0):内容用 contains 子串匹配而非全等——抽取措辞允许漂移,
// 锁的是"该记的记了、kind/matter 归类对、happened_at 对";负断言(not_contains)防错记。

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
)

// GoldenFixture 金样本:世界观快照 + 对话区间 + 脚本化 LLM 输出 + 期望终态。
type GoldenFixture struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	UserID      int64  `json:"user_id"`
	Snapshot    struct {
		Matters []struct {
			Title     string `json:"title"`
			StateDesc string `json:"state_desc"`
			Status    string `json:"status"`
		} `json:"matters"`
		// Loops 显式带 ID(如 101):既是快照展示的 [#编号],也是 scripted_output
		// loop_closes 的引用目标——ID 与消息自增位隔离,fixture 可读且确定。
		Loops []struct {
			ID      int64  `json:"id"`
			Content string `json:"content"`
		} `json:"loops"`
	} `json:"snapshot"`
	Messages []struct {
		Role      string    `json:"role"`
		Content   string    `json:"content"`
		CreatedAt time.Time `json:"created_at"`
	} `json:"messages"`
	ScriptedOutput json.RawMessage   `json:"scripted_output"`
	Expected       GoldenExpectation `json:"expected"`
}

// GoldenExpectation 期望终态(全部子串/归类断言)。
type GoldenExpectation struct {
	Matters            []ExpectedMatter    `json:"matters"`
	Facts              []ExpectedFact      `json:"facts"`
	LoopClosedContents []string            `json:"loop_closed_contents"` // 这些 content 的 loop 应已 closed
	LoopOpenContents   []string            `json:"loop_open_contents"`   // 应仍 open
	StyleNotes         []ExpectedStyleNote `json:"style_notes"`
	PreferenceChanges  []ExpectedPref      `json:"preference_changes"`
}

type ExpectedMatter struct {
	Title            string   `json:"title"`
	StateDescContains []string `json:"state_desc_contains"`
	Status           string   `json:"status"` // 空=不断言
}

type ExpectedFact struct {
	ContentContains    []string `json:"content_contains"`
	ContentNotContains []string `json:"content_not_contains"` // 负断言:防错记
	MatterTitle        string   `json:"matter_title"`          // 空=不断言挂靠
	HappenedAt         string   `json:"happened_at"`           // "2006-01-02" 前缀匹配;空=不断言
}

type ExpectedStyleNote struct {
	Dimension       string   `json:"dimension"`
	ContentContains []string `json:"content_contains"`
}

type ExpectedPref struct {
	ContentContains []string `json:"content_contains"`
}

// GoldenState 从 DB 读回的沉淀终态。
type GoldenState struct {
	Matters           []memorydomain.Matter
	Facts             []memorydomain.Memory // kind=fact
	Loops             []memorydomain.Memory // kind=loop(任意状态)
	StyleNotes        []memorydomain.StyleObservation
	PreferenceChanges []memorydomain.CognitionChange
}

// LoadGoldenFixture 从 JSON 文件加载金样本。
func LoadGoldenFixture(path string) (*GoldenFixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var f GoldenFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return &f, nil
}

// LoadGoldenState 读回该用户的沉淀终态(facts 含 happened_at/matter 挂靠)。
func LoadGoldenState(db *gorm.DB, userID int64) (*GoldenState, error) {
	st := &GoldenState{}
	if err := db.Where("user_id = ?", userID).Order("id").Find(&st.Matters).Error; err != nil {
		return nil, err
	}
	if err := db.Where("user_id = ? AND kind = ?", userID, memorydomain.MemoryKindFact).Order("id").Find(&st.Facts).Error; err != nil {
		return nil, err
	}
	if err := db.Where("user_id = ? AND kind = ?", userID, memorydomain.MemoryKindLoop).Order("id").Find(&st.Loops).Error; err != nil {
		return nil, err
	}
	if err := db.Where("user_id = ?", userID).Order("id").Find(&st.StyleNotes).Error; err != nil {
		return nil, err
	}
	if err := db.Where("user_id = ?", userID).Order("id").Find(&st.PreferenceChanges).Error; err != nil {
		return nil, err
	}
	return st, nil
}

// GoldenMatchResult 匹配结果:Fails 空 = 全部期望命中(测试断言点);
// Extra* 是 expected 之外多抽的条目(误抽候选,人工判——E2 报告展示,E1 不因此失败)。
type GoldenMatchResult struct {
	Passes                 []string
	Fails                  []string
	ExtraMatters           []string
	ExtraFacts             []string
	ExtraStyleNotes        []string
	ExtraPreferenceChanges []string
}

// MatchGolden 比对终态与期望。每条 expected 至少被 1 条实际条目命中(贪心消费,
// 一条实际只消费一次),剩余实际条目计为 Extra。
func MatchGolden(state *GoldenState, exp *GoldenExpectation) *GoldenMatchResult {
	res := &GoldenMatchResult{}

	// ---- matters ----
	matterUsed := make([]bool, len(state.Matters))
	for i, em := range exp.Matters {
		label := fmt.Sprintf("matters[%d] %q", i, em.Title)
		found := false
		for j, m := range state.Matters {
			if m.Title != em.Title || matterUsed[j] {
				continue
			}
			if !containsAll(m.StateDesc, em.StateDescContains) {
				continue
			}
			if em.Status != "" && m.Status != em.Status {
				continue
			}
			matterUsed[j] = true
			found = true
			break
		}
		if found {
			res.Passes = append(res.Passes, label)
		} else {
			res.Fails = append(res.Fails, label+" 未命中(或 state_desc/status 不符)")
		}
	}
	for j, m := range state.Matters {
		if !matterUsed[j] {
			res.ExtraMatters = append(res.ExtraMatters, fmt.Sprintf("%s: %s", m.Title, truncateRunesForReport(m.StateDesc, 80)))
		}
	}

	// ---- facts ----
	matterTitleByID := make(map[int64]string)
	for _, m := range state.Matters {
		matterTitleByID[m.ID] = m.Title
	}
	factUsed := make([]bool, len(state.Facts))
	for i, ef := range exp.Facts {
		label := fmt.Sprintf("facts[%d] contains %v", i, ef.ContentContains)
		found := false
		for j, f := range state.Facts {
			if factUsed[j] {
				continue
			}
			if !matchesFact(f, ef, matterTitleByID) {
				continue
			}
			factUsed[j] = true
			found = true
			break
		}
		if found {
			res.Passes = append(res.Passes, label)
		} else {
			res.Fails = append(res.Fails, label+" 未命中")
		}
	}
	for j, f := range state.Facts {
		if !factUsed[j] {
			res.ExtraFacts = append(res.ExtraFacts, truncateRunesForReport(f.Content, 100))
		}
	}

	// ---- loops ----
	for _, content := range exp.LoopClosedContents {
		if l := findLoop(state.Loops, content, memorydomain.MemoryLoopStatusClosed); l {
			res.Passes = append(res.Passes, "loop closed: "+content)
		} else {
			res.Fails = append(res.Fails, "loop 应已 closed 但没有: "+content)
		}
	}
	for _, content := range exp.LoopOpenContents {
		if l := findLoop(state.Loops, content, memorydomain.MemoryLoopStatusOpen); l {
			res.Passes = append(res.Passes, "loop open: "+content)
		} else {
			res.Fails = append(res.Fails, "loop 应仍 open 但没有: "+content)
		}
	}

	// ---- style notes / preference changes ----
	noteUsed := make([]bool, len(state.StyleNotes))
	for i, es := range exp.StyleNotes {
		label := fmt.Sprintf("style_notes[%d] [%s] contains %v", i, es.Dimension, es.ContentContains)
		found := false
		for j, n := range state.StyleNotes {
			if noteUsed[j] || n.Dimension != es.Dimension || !containsAll(n.Content, es.ContentContains) {
				continue
			}
			noteUsed[j] = true
			found = true
			break
		}
		if found {
			res.Passes = append(res.Passes, label)
		} else {
			res.Fails = append(res.Fails, label+" 未命中")
		}
	}
	for j, n := range state.StyleNotes {
		if !noteUsed[j] {
			res.ExtraStyleNotes = append(res.ExtraStyleNotes, fmt.Sprintf("[%s] %s", n.Dimension, truncateRunesForReport(n.Content, 80)))
		}
	}

	changeUsed := make([]bool, len(state.PreferenceChanges))
	for i, ec := range exp.PreferenceChanges {
		label := fmt.Sprintf("preference_changes[%d] contains %v", i, ec.ContentContains)
		found := false
		for j, c := range state.PreferenceChanges {
			if changeUsed[j] || !containsAll(c.Content, ec.ContentContains) {
				continue
			}
			changeUsed[j] = true
			found = true
			break
		}
		if found {
			res.Passes = append(res.Passes, label)
		} else {
			res.Fails = append(res.Fails, label+" 未命中")
		}
	}
	for j, c := range state.PreferenceChanges {
		if !changeUsed[j] {
			res.ExtraPreferenceChanges = append(res.ExtraPreferenceChanges, truncateRunesForReport(c.Content, 80))
		}
	}

	return res
}

// matchesFact 单条 fact 匹配:contains 全中 + not_contains 全不中 + 挂靠/时间可选断言。
func matchesFact(f memorydomain.Memory, ef ExpectedFact, matterTitleByID map[int64]string) bool {
	if !containsAll(f.Content, ef.ContentContains) {
		return false
	}
	for _, banned := range ef.ContentNotContains {
		if strings.Contains(f.Content, banned) {
			return false
		}
	}
	if ef.MatterTitle != "" {
		title := matterTitleByID[ptrValue(f.MatterID)]
		if title != ef.MatterTitle {
			return false
		}
	}
	if ef.HappenedAt != "" {
		if f.HappenedAt == nil || f.HappenedAt.Format("2006-01-02") != ef.HappenedAt {
			return false
		}
	}
	return true
}

func findLoop(loops []memorydomain.Memory, content, status string) bool {
	for _, l := range loops {
		if strings.Contains(l.Content, content) && l.LoopStatus == status {
			return true
		}
	}
	return false
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func ptrValue(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// truncateRunesForReport 报告展示截断(rune 安全,见 15 号截断教训)。
func truncateRunesForReport(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
