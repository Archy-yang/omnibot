package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"omnibot/internal/domain/conversation"
	memoryrepo "omnibot/internal/repository/memory"
	"omnibot/pkg/logger"

	"go.uber.org/zap"
)

// 沉淀管线(12-记忆系统技术方案 §7):
//
//	对话轮次结束 → NotifyTurn(异步,不阻塞对话) → 水位检查
//	  → pending ≥ 阈值 → per-user 串行执行一轮:
//	      区间对话原文 → LLM 纪要 → digests 落库
//	                                  → LLM 记忆提取 → memories 落库(M2-C)
//	      → 推进水位(失败不推进,下轮重试同一区间)
//
// 纪要/提取用 LLM 与 embedding 均按用户解析(M5.1 修订 §7.2:用户配置优先、系统默认兜底;
// 消耗用户配额、产出用户记忆,理应同源):管线只认 userID,解析逻辑在装配点。
// embedding 未配置或失败时纪要/记忆照常落库(仅无向量,读路径自动降级子串)。

// ConversationSource 对话消息区间读取(由 chat 消息仓储适配)。
type ConversationSource interface {
	GetLatestMessageID(userID int64) (int64, error)
	GetRangeByUserID(userID int64, afterID, toID int64) ([]*conversation.Message, error)
}

// ErrPipelineNoLLM 用户无可用 LLM 配置(用户未配置且系统默认也未装配)。
// 管线视作"本轮无事可做"静默跳过,不告警刷屏;用户配置后自动从当前区间开始沉淀。
var ErrPipelineNoLLM = errors.New("no llm available for pipeline")

// PipelineLLM 管线用 LLM(非流式补全;由装配点按用户解析:用户配置优先、系统默认兜底)。
type PipelineLLM interface {
	Complete(ctx context.Context, userID int64, system, user string) (string, error)
}

// DigestPipeline 沉淀管线。
type DigestPipeline struct {
	watermarkRepo memoryrepo.WatermarkRepository
	digestRepo    memoryrepo.DigestRepository // M6 起停止写入(digests 退役只读);保留注入供旧数据读取路径
	memoryRepo    memoryrepo.MemoryRepository
	matterRepo    memoryrepo.MatterRepository // M6:事项层(对账式沉淀的核心)
	source        ConversationSource
	llm           PipelineLLM       // nil = 管线禁用(LLM 未装配)
	embedding     EmbeddingProvider // 系统默认,可 nil(降级无向量)
	// embeddingResolver 用户级向量解析(M5.1,§5.3 同源):非 nil 且返回非 nil 时优先;
	// nil/解析为 nil → 回落系统默认 embedding。
	embeddingResolver func(userID int64) EmbeddingProvider
	threshold         int         // pending 消息数阈值
	maxBatchMessages  int         // 单轮最多沉淀的消息数(积压分块,防巨包请求)
	audit             DigestAudit // 留痕(M5.3):task+step 可观测,nil=仅日志
	inflight          sync.Map    // userID → struct{} (per-user 单飞标记)
}

// SetAudit 注入留痕适配器(装配点调用;不影响既有测试)。
func (p *DigestPipeline) SetAudit(a DigestAudit) {
	p.audit = a
}

const digestMaxBatchMessages = 40 // 单轮块上限:两倍默认阈值,兼顾摊销与请求体积

// SetEmbeddingResolver 注入用户级向量解析器(装配点调用;复用用户向量配置缓存)。
func (p *DigestPipeline) SetEmbeddingResolver(r func(userID int64) EmbeddingProvider) {
	p.embeddingResolver = r
}

// embeddingFor 本轮沉淀的向量 provider:用户解析优先,回落系统默认。
func (p *DigestPipeline) embeddingFor(userID int64) EmbeddingProvider {
	if p.embeddingResolver != nil {
		if ep := p.embeddingResolver(userID); ep != nil {
			return ep
		}
	}
	return p.embedding
}

func NewDigestPipeline(
	watermarkRepo memoryrepo.WatermarkRepository,
	digestRepo memoryrepo.DigestRepository,
	memoryRepo memoryrepo.MemoryRepository,
	matterRepo memoryrepo.MatterRepository,
	source ConversationSource,
	llm PipelineLLM,
	embedding EmbeddingProvider,
	threshold int,
) *DigestPipeline {
	if threshold <= 0 {
		threshold = 20 // 攒批越大摊销越低,且更贴近"按对话段落"语义(§7 修订)
	}
	return &DigestPipeline{
		maxBatchMessages: digestMaxBatchMessages,
		watermarkRepo:    watermarkRepo,
		digestRepo:       digestRepo,
		memoryRepo:       memoryRepo,
		matterRepo:       matterRepo,
		source:           source,
		llm:              llm,
		embedding:        embedding,
		threshold:        threshold,
	}
}

// NotifyTurn 对话轮次结束的钩子入口:异步触发,绝不阻塞对话,绝不 panic 外泄。
func (p *DigestPipeline) NotifyTurn(userID int64) {
	if p.llm == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.ErrorWithFields("memory: 沉淀管线 panic",
					zap.Int64("user_id", userID), zap.Any("recover", r))
			}
		}()
		ctx := context.Background()
		if err := p.RunOnce(ctx, userID); err != nil {
			logger.WarnWithFields("memory: 沉淀管线本轮未完成,水位未推进,下轮重试",
				zap.Int64("user_id", userID), zap.Error(err))
		}
	}()
}

// RunOnce 同步执行一轮沉淀(导出供测试与手动触发)。
// 返回 error 仅表示"本轮未完成"(水位未推进,下轮重试),调用方可安全忽略。
func (p *DigestPipeline) RunOnce(ctx context.Context, userID int64) error {
	if p.llm == nil {
		return nil
	}
	// per-user 单飞:同一用户串行,不同用户互不影响
	if _, loaded := p.inflight.LoadOrStore(userID, struct{}{}); loaded {
		return nil
	}
	defer p.inflight.Delete(userID)

	latest, err := p.source.GetLatestMessageID(userID)
	if err != nil {
		return fmt.Errorf("读最新消息 ID: %w", err)
	}
	wm, err := p.watermarkRepo.GetByUserID(userID)
	if err != nil {
		return fmt.Errorf("读水位: %w", err)
	}
	pending := latest - wm.LastDigestMsgID
	if pending <= 0 || pending < int64(p.threshold) {
		return nil
	}

	fromID := wm.LastDigestMsgID // 区间语义 (from, to]
	toID := latest
	messages, err := p.source.GetRangeByUserID(userID, fromID, toID)
	if err != nil {
		return fmt.Errorf("读区间消息: %w", err)
	}
	if len(messages) == 0 {
		// 消息可能被清理:直接推进水位避免死循环
		return p.watermarkRepo.Upsert(userID, toID)
	}
	// 积压分块(M5.1 硬化):首次沉淀/长期停用后的全量积压,不把巨包打给 LLM。
	// 每轮只沉淀一块,水位推进到块尾,下一轮继续。
	if len(messages) > p.maxBatchMessages {
		messages = messages[:p.maxBatchMessages]
		toID = messages[len(messages)-1].ID
	}
	transcript := buildTranscript(messages)
	// 对账式输入(M6):世界观快照 + 新增对话——LLM 必须看到已有事项才能增量更新
	userPayload := p.buildWorldViewSnapshot(userID) + "\n\n" + transcript

	// 单次 LLM 调用完成对账(事项覆写 + 分层事实提取)。
	// 调用失败或结果 schema 非法 → 整批作废,水位不动,下轮重试同一区间。
	// 用户无可用配置(ErrPipelineNoLLM)→ 静默跳过(配置后从当前区间自动开始)。
	llmStart := time.Now()
	resp, err := p.llm.Complete(ctx, userID, pipelineSystemPrompt, userPayload)
	if err != nil {
		if errors.Is(err, ErrPipelineNoLLM) {
			logger.InfoWithFields("memory: 用户无可用 LLM 配置,本轮沉淀跳过",
				zap.Int64("user_id", userID))
			return nil // 不留痕:没有发生任何真实工作,避免每轮刷 cancelled 任务
		}
	}

	// 留痕(M5.3):LLM 返回后(真实工作已发生/已失败)建 task,失败降级为仅日志(审计是旁路)
	var taskID int64
	if p.audit != nil {
		if id, err := p.audit.BeginTask(userID, fromID+1, toID, len(messages)); err != nil {
			logger.WarnWithFields("memory: 沉淀留痕建任务失败,本轮降级为仅日志",
				zap.Int64("user_id", userID), zap.Error(err))
		} else {
			taskID = id
		}
	}
	if p.audit != nil && taskID != 0 {
		stepStatus := "success"
		if err != nil {
			stepStatus = "error"
		}
		if stepErr := p.audit.RecordStep(taskID, userID, 0, "llm_call", "memory.digest",
			userPayload, resp, stepStatus, time.Since(llmStart).Milliseconds()); stepErr != nil {
			logger.WarnWithFields("memory: 沉淀留痕记步骤失败",
				zap.Int64("user_id", userID), zap.Error(stepErr))
		}
	}
	if err != nil {
		p.endAuditTask(taskID, "failed", "", fmt.Sprintf("沉淀调用失败: %v", err))
		return fmt.Errorf("沉淀调用失败: %w", err)
	}
	var parsed reconcileResult
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		p.endAuditTask(taskID, "failed", "", fmt.Sprintf("对账结果 schema 非法: %v", err))
		return fmt.Errorf("对账结果 schema 非法,整批作废: %w", err)
	}

	// 对账执行(M6):事项 upsert(覆写状态)+ 原子记忆分层落库。
	// digests 表退役(只读保留),不再写入切片纪要。
	mattersUpserted, created, updated := p.reconcile(userID, parsed, fromID, toID)
	if p.audit != nil && taskID != 0 {
		respJSON, _ := json.Marshal(map[string]int{
			"matters": mattersUpserted, "created": created, "updated": updated,
		})
		if stepErr := p.audit.RecordStep(taskID, userID, 1, "tool_call", "digest.persist",
			fmt.Sprintf("区间 (%d,%d] 消息 %d 条", fromID, toID, len(messages)),
			string(respJSON), "success", 0); stepErr != nil {
			logger.WarnWithFields("memory: 沉淀留痕记步骤失败",
				zap.Int64("user_id", userID), zap.Error(stepErr))
		}
	}

	// 推进水位
	if err := p.watermarkRepo.Upsert(userID, toID); err != nil {
		p.endAuditTask(taskID, "failed", fmt.Sprintf("事项 %d,新增 %d,更新 %d", mattersUpserted, created, updated),
			fmt.Sprintf("推进水位: %v", err))
		return fmt.Errorf("推进水位: %w", err)
	}
	artifact := fmt.Sprintf("事项更新 %d,新增记忆 %d,更新记忆 %d", mattersUpserted, created, updated)
	p.endAuditTask(taskID, "completed", artifact, "")
	return nil
}

// endAuditTask 收尾留痕(best-effort;taskID=0 或留痕失败仅记日志)。
func (p *DigestPipeline) endAuditTask(taskID int64, status, artifact, errMsg string) {
	if p.audit == nil || taskID == 0 {
		return
	}
	if err := p.audit.EndTask(taskID, status, artifact, errMsg); err != nil {
		logger.WarnWithFields("memory: 沉淀留痕收尾失败",
			zap.Int64("task_id", taskID), zap.Error(err))
	}
}

// buildTranscript 把区间消息拼成 LLM 可读的对话原文。
func buildTranscript(messages []*conversation.Message) string {
	var b strings.Builder
	b.WriteString("以下是用户与助手的一段对话原文(按时间正序):\n\n")
	for _, m := range messages {
		role := "用户"
		if m.Role == conversation.RoleAssistant {
			role = "助手"
		}
		fmt.Fprintf(&b, "[消息#%d|%s] %s\n", m.ID, role, m.Content)
	}
	return b.String()
}
