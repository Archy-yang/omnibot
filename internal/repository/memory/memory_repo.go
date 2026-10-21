package memory

import (
	"sort"
	"time"

	memorydomain "omnibot/internal/domain/memory"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MemoryRepository interface {
	Create(memory *memorydomain.Memory) error
	ListByUserID(userID int64) ([]*memorydomain.Memory, error)
	// ListManualByUserID 仅手动记忆(id 升序)——常驻注入用(注入分层:手动常驻,自动走工具)。
	ListManualByUserID(userID int64) ([]*memorydomain.Memory, error)
	// CountByUserIDAndSource 按来源计数(注入的存在性提示行用)。
	CountByUserIDAndSource(userID int64, source string) (int64, error)
	DeleteByUserID(userID int64) error
	// DeleteByUserIDAndSource 按来源清空(记忆抽屉双 tab 各清各的,注入分层)。
	DeleteByUserIDAndSource(userID int64, source string) error
	GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error)
	GetByID(id int64, userID int64) (*memorydomain.Memory, error)
	DeleteByID(id int64, userID int64) (bool, error)
	UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error)
	// UpdateContentEmbeddingByID 沉淀管线疑似冲突时原位更新(内容+向量+模型标记,§7.3)。
	UpdateContentEmbeddingByID(id int64, userID int64, content string, embedding []float32, embeddingModel string) error
	// CreateLinks 批量写入记忆↔消息溯源映射(M5.2;重复对幂等跳过)。
	CreateLinks(links []memorydomain.MemoryMessageLink) error
	// ReplaceLinksForMemory 原位更新记忆时整体替换其溯源映射(空切片=清空)。
	ReplaceLinksForMemory(memoryID int64, messageIDs []int64) error
	// ListByUserIDAndMatter 某事项挂靠的记忆(M6.2 事项全景检索用;创建时间升序)。
	ListByUserIDAndMatter(userID int64, matterID int64) ([]*memorydomain.Memory, error)
	// ListOpenLoops 未决 loop(M8 世界观快照用):kind=loop 且 loop_status=open,
	// 两端各取一半(最旧+最新)最多 limit 条,输出 created_at 升序(架构复评 E2)。
	ListOpenLoops(userID int64, limit int) ([]*memorydomain.Memory, error)
	// TransitionLoopStatus loop 生命周期迁移(M8 §14.2.2):仅当当前状态=fromStatus 时置为
	// toStatus(只对 kind=loop 生效)。返回是否发生迁移(幂等:重复关闭/重开返回 false)。
	TransitionLoopStatus(id int64, userID int64, fromStatus, toStatus string) (bool, error)
	// CloseOpenLoopsByMatter 档案完结级联(28号 N1):关闭该档案下全部 open loops,
	// 返回关闭条数(孤儿 loop——matter_id 为空——不在此列,走 CloseLoopByQuery 单独收)。
	CloseOpenLoopsByMatter(userID, matterID int64) (int64, error)
	// ListPinnedAutoByUserID 置顶的自动记忆(M8.3 常驻 core;pinned_at 倒序,新近置顶优先)。
	ListPinnedAutoByUserID(userID int64) ([]*memorydomain.Memory, error)
	// ListJournal 时间轴查询(21号 §5.1):有效时间=COALESCE(happened_at,created_at) 降序;
	// kinds 空=不过滤;from/to 过滤有效时间窗;afterTime/afterID 为游标(取严格早于该键的下一页,零值=首页)。
	ListJournal(userID int64, kinds []string, from, to, afterTime *time.Time, afterID int64, limit int) ([]*memorydomain.Memory, error)
	// ListLinksForMemories 批量取溯源映射(21号溯源拼装);ids 空 → 空表。
	ListLinksForMemories(memoryIDs []int64) ([]memorydomain.MemoryMessageLink, error)
	// CountByMatter 各事项挂靠记忆数(21号档案列表计数);无挂靠的 matter 不在返回中。
	CountByMatter(userID int64) (map[int64]int64, error)
	// ListByMatter 档案时间线(21号 §5.2):pinned 前置,其余按有效时间升序;user+matter 双重隔离。
	ListByMatter(userID int64, matterID int64) ([]*memorydomain.Memory, error)
	// ListBlacklistPatterns 黑名单关键词集(22号 §5.2:沉淀/注入/检索三处过滤共用)。
	ListBlacklistPatterns(userID int64) ([]string, error)
	// 风格与认知证据(24号 §3):管线落库 + 画像视图。
	CreateStyleObservations(rows []memorydomain.StyleObservation) error
	CreateCognitionChanges(rows []memorydomain.CognitionChange) error
	ListStyleObservations(userID int64, limit int) ([]*memorydomain.StyleObservation, error)
	ListCognitionChanges(userID int64, limit int) ([]*memorydomain.CognitionChange, error)
	DeleteStyleObservation(userID, id int64) (bool, error)
	DeleteCognitionChange(userID, id int64) (bool, error)
	// SetPinned 置顶/取消置顶(M8.3)。返回是否命中(他人/不存在 → false)。
	SetPinned(id int64, userID int64, pinned bool) (bool, error)
}

type memoryRepository struct {
	db *gorm.DB
}

func NewMemoryRepository(db *gorm.DB) MemoryRepository {
	return &memoryRepository{db: db}
}

func (r *memoryRepository) Create(memory *memorydomain.Memory) error {
	return r.db.Create(memory).Error
}

func (r *memoryRepository) ListByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

// ListManualByUserID 仅手动记忆(id 升序)——常驻注入用。
func (r *memoryRepository) ListManualByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ? AND source = ?", userID, memorydomain.MemorySourceManual).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

// CountByUserIDAndSource 按来源计数。
func (r *memoryRepository) CountByUserIDAndSource(userID int64, source string) (int64, error) {
	var count int64
	err := r.db.Model(&memorydomain.Memory{}).
		Where("user_id = ? AND source = ?", userID, source).
		Count(&count).Error
	return count, err
}

func (r *memoryRepository) DeleteByUserID(userID int64) error {
	return r.db.Where("user_id = ?", userID).Delete(&memorydomain.Memory{}).Error
}

// DeleteByUserIDAndSource 按来源清空。
func (r *memoryRepository) DeleteByUserIDAndSource(userID int64, source string) error {
	return r.db.Where("user_id = ? AND source = ?", userID, source).
		Delete(&memorydomain.Memory{}).Error
}

func (r *memoryRepository) GetRecentByUserID(userID int64, limit int) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ?", userID).
		Order("id DESC").
		Limit(limit).
		Find(&memories).Error
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(memories)-1; i < j; i, j = i+1, j-1 {
		memories[i], memories[j] = memories[j], memories[i]
	}

	return memories, nil
}

func (r *memoryRepository) GetByID(id int64, userID int64) (*memorydomain.Memory, error) {
	var memory memorydomain.Memory
	err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&memory).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	return &memory, err
}

func (r *memoryRepository) DeleteByID(id int64, userID int64) (bool, error) {
	result := r.db.Where("id = ? AND user_id = ?", id, userID).Delete(&memorydomain.Memory{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (r *memoryRepository) UpdateContentByID(id int64, userID int64, content string) (*memorydomain.Memory, error) {
	result := r.db.Model(&memorydomain.Memory{}).
		Where("id = ? AND user_id = ?", id, userID).
		Updates(map[string]interface{}{
			"content":    content,
			"updated_at": time.Now(),
		})
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, nil
	}
	return r.GetByID(id, userID)
}

// UpdateContentEmbeddingByID 沉淀管线疑似冲突时原位更新(内容+向量+模型标记,§7.3)。
// embedding 为 nil 时只更新内容并清空向量(内容变了旧向量不可再用)。
// 取行再 Save:GORM 的 map Updates 不走 serializer(JSON 向量列),struct Save 才会。
func (r *memoryRepository) UpdateContentEmbeddingByID(id int64, userID int64, content string, embedding []float32, embeddingModel string) error {
	var mem memorydomain.Memory
	if err := r.db.Where("id = ? AND user_id = ?", id, userID).First(&mem).Error; err != nil {
		return err
	}
	mem.Content = content
	mem.Embedding = embedding
	mem.EmbeddingModel = embeddingModel
	mem.UpdatedAt = time.Now()
	return r.db.Save(&mem).Error
}

// CreateLinks 批量写入记忆↔消息溯源映射(M5.2):唯一索引去重,重复对幂等跳过。
func (r *memoryRepository) CreateLinks(links []memorydomain.MemoryMessageLink) error {
	if len(links) == 0 {
		return nil
	}
	return r.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&links).Error
}

// ReplaceLinksForMemory 原位更新记忆时整体替换其溯源映射(事务:删旧+写新;空切片=清空)。
func (r *memoryRepository) ReplaceLinksForMemory(memoryID int64, messageIDs []int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("memory_id = ?", memoryID).Delete(&memorydomain.MemoryMessageLink{}).Error; err != nil {
			return err
		}
		links := memorydomain.NewMemoryMessageLinks(memoryID, messageIDs)
		if len(links) == 0 {
			return nil // GORM 拒绝 Create 空切片
		}
		return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&links).Error
	})
}

// ListByUserIDAndMatter 某事项挂靠的记忆(M6.2 事项全景检索用)。
func (r *memoryRepository) ListByUserIDAndMatter(userID int64, matterID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ? AND matter_id = ?", userID, matterID).
		Order("id ASC").
		Find(&memories).Error
	return memories, err
}

// ListOpenLoops 未决 loop(M8 世界观快照用):两端各取一半,最多 limit 条(架构复评 E2)。
// 只取最旧会让最新 loop(最可能刚被了结的)永远进不了快照,反之亦然——两端覆盖两个极端。
// 返回顺序:created_at ASC(稳定,便于 prompt 内编号引用)。
func (r *memoryRepository) ListOpenLoops(userID int64, limit int) ([]*memorydomain.Memory, error) {
	if limit <= 0 {
		limit = defaultOpenLoopLimit
	}
	half := limit/2 + limit%2
	base := func() *gorm.DB {
		return r.db.Where("user_id = ? AND kind = ? AND loop_status = ?",
			userID, memorydomain.MemoryKindLoop, memorydomain.MemoryLoopStatusOpen)
	}
	var oldest, newest []*memorydomain.Memory
	if err := base().Order("created_at ASC").Limit(half).Find(&oldest).Error; err != nil {
		return nil, err
	}
	if err := base().Order("created_at DESC").Limit(limit - half).Find(&newest).Error; err != nil {
		return nil, err
	}
	// 合并去重(总量 ≤ limit 时两端重叠),按 created_at ASC 稳定输出
	seen := make(map[int64]struct{}, limit)
	merged := make([]*memorydomain.Memory, 0, limit)
	for _, list := range [][]*memorydomain.Memory{oldest, newest} {
		for _, m := range list {
			if _, dup := seen[m.ID]; dup {
				continue
			}
			seen[m.ID] = struct{}{}
			merged = append(merged, m)
		}
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].CreatedAt.Before(merged[j].CreatedAt) })
	return merged, nil
}

// defaultOpenLoopLimit 快照未决 loop 兜底上限(与 service 层 snapshotMaxLoops 语义一致;
// repository 不反向依赖 service,故本地定义。现有唯一调用点始终显式传参)。
const defaultOpenLoopLimit = 20

// TransitionLoopStatus loop 生命周期迁移(M8):CAS 语义,仅 fromStatus → toStatus。
// 只对 kind=loop 生效;返回是否发生迁移(幂等)。
func (r *memoryRepository) TransitionLoopStatus(id int64, userID int64, fromStatus, toStatus string) (bool, error) {
	res := r.db.Model(&memorydomain.Memory{}).
		Where("id = ? AND user_id = ? AND kind = ? AND loop_status = ?", id, userID, memorydomain.MemoryKindLoop, fromStatus).
		Update("loop_status", toStatus)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// CloseOpenLoopsByMatter 档案完结级联(28号 N1):matter → done 时其下 open loops 联动 closed。
func (r *memoryRepository) CloseOpenLoopsByMatter(userID, matterID int64) (int64, error) {
	res := r.db.Model(&memorydomain.Memory{}).
		Where("user_id = ? AND matter_id = ? AND kind = ? AND loop_status = ?",
			userID, matterID, memorydomain.MemoryKindLoop, memorydomain.MemoryLoopStatusOpen).
		Update("loop_status", memorydomain.MemoryLoopStatusClosed)
	if res.Error != nil {
		return 0, res.Error
	}
	return res.RowsAffected, nil
}

// ListPinnedAutoByUserID 置顶的自动记忆(M8.3 常驻 core;pinned_at 倒序)。
func (r *memoryRepository) ListPinnedAutoByUserID(userID int64) ([]*memorydomain.Memory, error) {
	var memories []*memorydomain.Memory
	err := r.db.Where("user_id = ? AND source = ? AND pinned = ?", userID, memorydomain.MemorySourceAuto, true).
		Order("pinned_at DESC").
		Find(&memories).Error
	return memories, err
}

// SetPinned 置顶/取消置顶(M8.3):置顶记录时间,取消置 NULL。返回是否命中。
// 取消置 NULL 须用 gorm.Expr("NULL"):typed nil 指针在本驱动下会被更新跳过。
func (r *memoryRepository) SetPinned(id int64, userID int64, pinned bool) (bool, error) {
	now := time.Now()
	var pinnedAt interface{} = gorm.Expr("NULL")
	if pinned {
		pinnedAt = now
	}
	res := r.db.Model(&memorydomain.Memory{}).
		Where("id = ? AND user_id = ?", id, userID).
		Select("pinned", "pinned_at", "updated_at").
		Updates(map[string]interface{}{
			"pinned":     pinned,
			"pinned_at":  pinnedAt,
			"updated_at": now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// ListJournal 时间轴查询(21号 §5.1):有效时间=COALESCE(happened_at,created_at) 降序。
// 排序/过滤/游标都走同一有效时间表达式,SQLite 与 PG 均支持的写法(布尔排序 + OR 展开谓词)。
func (r *memoryRepository) ListJournal(userID int64, kinds []string, from, to, afterTime *time.Time, afterID int64, limit int) ([]*memorydomain.Memory, error) {
	const eff = "COALESCE(happened_at, created_at)"
	q := r.db.Where("user_id = ?", userID)
	if len(kinds) > 0 {
		q = q.Where("kind IN ?", kinds)
	}
	if from != nil {
		q = q.Where(eff+" >= ?", *from)
	}
	if to != nil {
		q = q.Where(eff+" <= ?", *to)
	}
	if afterTime != nil {
		q = q.Where("("+eff+" < ? OR ("+eff+" = ? AND id < ?))", *afterTime, *afterTime, afterID)
	}
	if limit <= 0 {
		limit = 50
	}
	var out []*memorydomain.Memory
	err := q.Order(eff + " DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *memoryRepository) ListLinksForMemories(memoryIDs []int64) ([]memorydomain.MemoryMessageLink, error) {
	if len(memoryIDs) == 0 {
		return nil, nil
	}
	var links []memorydomain.MemoryMessageLink
	err := r.db.Where("memory_id IN ?", memoryIDs).Find(&links).Error
	return links, err
}

func (r *memoryRepository) CountByMatter(userID int64) (map[int64]int64, error) {
	type row struct {
		MatterID int64
		Cnt      int64
	}
	var rows []row
	err := r.db.Model(&memorydomain.Memory{}).
		Select("matter_id, COUNT(*) as cnt").
		Where("user_id = ? AND matter_id IS NOT NULL", userID).
		Group("matter_id").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.MatterID] = r.Cnt
	}
	return out, nil
}

// ListByMatter 档案时间线(21号 §5.2):pinned 前置,其余按有效时间升序。
func (r *memoryRepository) ListBlacklistPatterns(userID int64) ([]string, error) {
	var rows []memorydomain.MemoryBlacklist
	if err := r.db.Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Pattern)
	}
	return out, nil
}

func (r *memoryRepository) ListByMatter(userID int64, matterID int64) ([]*memorydomain.Memory, error) {
	const eff = "COALESCE(happened_at, created_at)"
	var out []*memorydomain.Memory
	err := r.db.Where("user_id = ? AND matter_id = ?", userID, matterID).
		Order("pinned DESC, " + eff + " ASC, id ASC").
		Find(&out).Error
	return out, err
}

// ---- 风格与认知证据层(24号 §3) ----

func (r *memoryRepository) CreateStyleObservations(rows []memorydomain.StyleObservation) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.Create(&rows).Error
}

func (r *memoryRepository) CreateCognitionChanges(rows []memorydomain.CognitionChange) error {
	if len(rows) == 0 {
		return nil
	}
	return r.db.Create(&rows).Error
}

func (r *memoryRepository) ListStyleObservations(userID int64, limit int) ([]*memorydomain.StyleObservation, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []*memorydomain.StyleObservation
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *memoryRepository) ListCognitionChanges(userID int64, limit int) ([]*memorydomain.CognitionChange, error) {
	if limit <= 0 {
		limit = 50
	}
	var out []*memorydomain.CognitionChange
	err := r.db.Where("user_id = ?", userID).Order("created_at DESC, id DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (r *memoryRepository) DeleteStyleObservation(userID, id int64) (bool, error) {
	res := r.db.Where("user_id = ? AND id = ?", userID, id).Delete(&memorydomain.StyleObservation{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func (r *memoryRepository) DeleteCognitionChange(userID, id int64) (bool, error) {
	res := r.db.Where("user_id = ? AND id = ?", userID, id).Delete(&memorydomain.CognitionChange{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
