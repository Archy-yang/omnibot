package memory

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	memorydomain "omnibot/internal/domain/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 对账式沉淀执行(M6,助理人视角):解析 LLM 的增量对账输出,
// 事项 upsert(覆写状态) + 原子记忆分层落库(挂 matter + 溯源 links)。
// 复用 M5 的全部机制:溯源区间校验/余弦去重/唯一索引幂等/留痕计数。

// reconcileResult LLM 对账输出的 schema(与 pipelineSystemPrompt 对齐)。
// M8 §14.2.2:loop 生命周期——loop_closes 关闭快照中列出的未决 loop,
// loop_reopens 重开明确重新托付的 closed loop(仅快照可引用,防幻觉)。
type reconcileResult struct {
	MatterUpdates []matterUpdate  `json:"matter_updates"`
	Facts         []factCandidate `json:"facts"`
	LoopCloses    []int64         `json:"loop_closes"`
	LoopReopens   []int64         `json:"loop_reopens"`
}

type matterUpdate struct {
	Title            string  `json:"title"`
	StateDesc        string  `json:"state_desc"`
	Status           string  `json:"status"`
	SourceMessageIDs []int64 `json:"source_message_ids"`
}

type factCandidate struct {
	Content          string  `json:"content"`
	Kind             string  `json:"kind"`
	MatterTitle      string  `json:"matter_title"`
	HappenedAt       string  `json:"happened_at"` // 21号:事件发生时间(LLM 换算为绝对时间;缺失/非法由 parseHappenedAt 容错为 NULL)
	SourceMessageIDs []int64 `json:"source_message_ids"`
}

// 快照上限(§14.2.2 成本护栏):防事项/loop 累积后快照无限膨胀推高沉淀 token。
const (
	snapshotMaxMatters = 50
	snapshotMaxLoops   = 20
)

// buildWorldViewSnapshot 世界观快照:活跃事项清单(标题+当前状态,updated_at 倒序、
// 超窗截断并提示)+ 未决 loops(带 ID,最旧优先)。
// LLM 必须看到已有事项才能做增量更新,title 从快照里引用以防同事项裂名;
// loop 关闭/重开指令只允许引用快照中列出的 [#id]。
func (p *DigestPipeline) buildWorldViewSnapshot(userID int64) string {
	var b strings.Builder
	b.WriteString("【世界观快照】\n")

	b.WriteString("当前记住的事项:\n")
	matters, err := p.matterRepo.ListActiveByUserID(userID)
	if err != nil {
		b.WriteString("(读取失败,按无事项处理)\n") // 快照读失败不阻断沉淀
	} else if len(matters) == 0 {
		b.WriteString("(无)\n")
	} else {
		shown := matters
		if len(shown) > snapshotMaxMatters {
			shown = shown[:snapshotMaxMatters]
		}
		for _, m := range shown {
			b.WriteString("- ")
			b.WriteString(m.Title)
			b.WriteString(": ")
			b.WriteString(m.StateDesc)
			b.WriteString("\n")
		}
		if remaining := len(matters) - len(shown); remaining > 0 {
			fmt.Fprintf(&b, "(另有 %d 个事项未列出)\n", remaining)
		}
	}

	b.WriteString("未决承诺/待办(loops):\n")
	loops, err := p.memoryRepo.ListOpenLoops(userID, snapshotMaxLoops)
	if err != nil {
		b.WriteString("(读取失败)\n")
	} else if len(loops) == 0 {
		b.WriteString("(无)\n")
	} else {
		for _, l := range loops {
			fmt.Fprintf(&b, "- [#%d] %s\n", l.ID, l.Content)
		}
	}
	return b.String()
}

// reconcileStats 对账执行统计(留痕用)。
type reconcileStats struct {
	MattersUpserted int
	MemoriesCreated int
	MemoriesUpdated int
	LoopsClosed     int
	LoopsReopened   int
}

// reconcile 执行对账:先 upsert 事项(建立 title→id 映射),再落 facts,最后处理 loop 生命周期。
// fromTime/toTime 为沉淀区间的时间锚(21号:事件时间抽取的合法窗口 [from-1年, to+24h])。
func (p *DigestPipeline) reconcile(
	userID int64,
	result reconcileResult,
	fromID, toID int64,
	fromTime, toTime time.Time,
) reconcileStats {
	var stats reconcileStats
	// 事项层:覆写式更新。title→id 映射供 facts 挂靠(快照已有 + 本轮新建/更新)
	titleToID := make(map[string]int64)
	existingMatters, _ := p.matterRepo.ListActiveByUserID(userID)
	for _, m := range existingMatters {
		titleToID[m.Title] = m.ID
	}

	for _, mu := range result.MatterUpdates {
		title := strings.TrimSpace(mu.Title)
		if title == "" {
			continue
		}
		validIDs := validSourceIDs(memoryCandidate{SourceMessageIDs: mu.SourceMessageIDs}, fromID, toID)
		var lastMsgID int64
		if len(validIDs) > 0 {
			lastMsgID = validIDs[len(validIDs)-1]
		}
		matter := &memorydomain.Matter{
			UserID:    userID,
			Title:     title,
			StateDesc: strings.TrimSpace(mu.StateDesc),
			Status:    memorydomain.NormalizeStatus(mu.Status),
			LastMsgID: lastMsgID,
		}
		// 事项向量化(Title+StateDesc,M6.2 事项命中检索用);失败仅无向量
		if emb := p.embeddingFor(userID); emb != nil {
			if vecs, err := emb.Embed(context.Background(), []string{title + " " + matter.StateDesc}); err == nil && len(vecs) == 1 {
				matter.Embedding = vecs[0]
				matter.EmbeddingModel = emb.Name()
			} else {
				logger.WarnWithFields("memory: 事项向量化失败,落库为无向量",
					zap.Int64("user_id", userID), zap.String("title", title), zap.Error(err))
			}
		}
		if err := p.matterRepo.UpsertByTitle(matter); err != nil {
			logger.WarnWithFields("memory: 事项 upsert 失败,跳过该事项",
				zap.Int64("user_id", userID), zap.String("title", title), zap.Error(err))
			continue
		}
		stats.MattersUpserted++
		// upsert 后重新取行拿 ID(新建时由 DB 生成;已有时映射可能原本没有——
		// 例如之前 done/archived 的事项重新活跃,不在 ListActive 里)
		if m, err := p.matterRepo.GetByTitle(userID, title); err == nil && m != nil {
			titleToID[title] = m.ID
		}
	}

	// 原子层:分层落库(复用余弦去重/冲突更新/links)
	stats.MemoriesCreated, stats.MemoriesUpdated = p.applyFacts(context.Background(), userID, result.Facts, titleToID, fromID, toID, fromTime, toTime)

	// loop 生命周期(M8 §14.2.2):关闭/重开只认 DB 中的真实状态(CAS 迁移)。
	// 「未命中」(ID 不存在/他人/状态不符)是有意静默——幂等,防幻觉关闭;
	// 但「DB 错误」必须可见:水位随后照常推进,该指令不会重试,静默即永久丢失(架构复评 P2-6)。
	applyLoop := func(ids []int64, from, to string, counter *int, action string) {
		for _, id := range ids {
			ok, err := p.memoryRepo.TransitionLoopStatus(id, userID, from, to)
			if err != nil {
				logger.WarnWithFields("memory: loop 状态迁移失败,该指令本轮丢失(水位仍会推进)",
					zap.Int64("user_id", userID), zap.Int64("memory_id", id),
					zap.String("action", action), zap.Error(err))
				continue
			}
			if ok {
				*counter++
			}
		}
	}
	applyLoop(result.LoopCloses, memorydomain.MemoryLoopStatusOpen, memorydomain.MemoryLoopStatusClosed, &stats.LoopsClosed, "close")
	applyLoop(result.LoopReopens, memorydomain.MemoryLoopStatusClosed, memorydomain.MemoryLoopStatusOpen, &stats.LoopsReopened, "reopen")
	return stats
}

// applyFacts 原子信息落库:过滤 → 溯源校验 → 事件时间 → 挂 matter → 嵌入 → 去重裁决 → 落库。
// 返回 (新增数, 原位更新数)。
func (p *DigestPipeline) applyFacts(
	ctx context.Context,
	userID int64,
	facts []factCandidate,
	titleToID map[string]int64,
	fromID, toID int64,
	fromTime, toTime time.Time,
) (created, updated int) {
	if len(facts) == 0 {
		return 0, 0
	}
	existing, err := p.memoryRepo.ListByUserID(userID)
	if err != nil {
		logger.WarnWithFields("memory: 读既有记忆失败,放弃本批提取",
			zap.Int64("user_id", userID), zap.Error(err))
		return 0, 0
	}
	var currentModel string
	emb := p.embeddingFor(userID)
	if emb != nil {
		currentModel = emb.Name()
	}

	blacklistPatterns, _ := p.memoryRepo.ListBlacklistPatterns(userID) // 22号:沉淀黑名单
	for _, f := range facts {
		content := strings.TrimSpace(f.Content)
		if content == "" || utf8.RuneCountInString(content) > MaxMemoryContentLength {
			continue
		}
		if containsBlacklisted(content, blacklistPatterns) {
			continue // 用户禁止沉淀的内容静默跳过(水位照常推进)
		}
		validIDs := validSourceIDs(memoryCandidate{SourceMessageIDs: f.SourceMessageIDs}, fromID, toID)
		var sourceMsgID *int64
		if len(validIDs) > 0 {
			id := validIDs[0] // 首个合法来源作主指针(展示兼容)
			sourceMsgID = &id
		}
		// 挂靠事项:title 必须命中映射(快照已有或本轮 upsert),否则不挂——宁缺毋错
		var matterID *int64
		if mt := strings.TrimSpace(f.MatterTitle); mt != "" {
			if id, ok := titleToID[mt]; ok {
				matterID = &id
			}
		}
		// 事件时间(21号):解析容错,坏值 NULL;冲突更新路径不改写既有时间
		happenedAt := parseHappenedAt(f.HappenedAt, fromTime, toTime)

		// 嵌入候选(失败 → 无向量,仍可落库,读路径降级子串)
		var vec []float32
		if emb != nil {
			if vecs, err := emb.Embed(ctx, []string{content}); err == nil && len(vecs) == 1 {
				vec = vecs[0]
			} else {
				logger.WarnWithFields("memory: 候选记忆向量化失败,落库为无向量",
					zap.Int64("user_id", userID), zap.Error(err))
			}
		}

		dupID, action := classifyCandidate(existing, vec, currentModel, content)
		switch action {
		case candidateSkip:
			continue
		case candidateUpdate:
			if err := p.memoryRepo.UpdateContentEmbeddingByID(dupID, userID, content, vec, currentModel); err != nil {
				logger.WarnWithFields("memory: 疑似冲突更新失败,按新增处理",
					zap.Int64("user_id", userID), zap.Int64("memory_id", dupID), zap.Error(err))
				p.createAutoMemory(userID, content, sourceMsgID, validIDs, vec, currentModel, f.Kind, matterID, happenedAt, &existing)
				created++
			} else {
				if err := p.memoryRepo.ReplaceLinksForMemory(dupID, validIDs); err != nil {
					logger.WarnWithFields("memory: 溯源映射替换失败",
						zap.Int64("user_id", userID), zap.Int64("memory_id", dupID), zap.Error(err))
				}
				updateExistingInPlace(existing, dupID, content, vec, currentModel)
				updated++
			}
		default:
			p.createAutoMemory(userID, content, sourceMsgID, validIDs, vec, currentModel, f.Kind, matterID, happenedAt, &existing)
			created++
		}
	}
	return created, updated
}
