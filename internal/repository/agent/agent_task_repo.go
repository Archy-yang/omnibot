package agent

import (
	"gorm.io/gorm"

	"omnibot/internal/domain/agent"
)

// AgentTaskRepository 后台 Agent 任务仓储接口(08 技术方案 §4.2)。
type AgentTaskRepository interface {
	Create(task *agent.AgentTask) error
	GetByID(id int64) (*agent.AgentTask, error)
	// TransitionStatus CAS 状态迁移(Phase 5,16-路线图 §11/§12):
	// UPDATE ... WHERE id=? AND status=from,仅当当前状态等于 from 才生效。
	// 迁移先过 domainagent.CanTransition 状态机表,非法迁移直接拒绝(false, nil)。
	// 返回 false = 迁移未发生(并发竞争或非法迁移),调用方据此决策,不作为错误。
	// completed 填 artifact,failed 填 errorMsg;时间戳随目标状态自动记。
	TransitionStatus(id int64, from, to string, artifact *string, errorMsg *string) (bool, error)
	// TransitionStatusWithEvent CAS 状态迁移 + 任务事件同一事务(Phase 7a,16-路线图 §14):
	// UPDATE status/version + INSERT task_event 原子提交——状态与事件史强一致,不会出现
	// "迁移成功但事件丢失"。事件 sequence = 迁移后的 task.version(version 随迁移 +1)。
	// CAS 失败或非法迁移返回 false,事务回滚、不产生事件。
	// eventType/source 语义同 TaskEvent 常量(EventTask* / "main"/"sub")。
	TransitionStatusWithEvent(id int64, from, to string, artifact *string, errorMsg *string, eventType, source string) (bool, error)
	// CreateWithEvent 建任务与 submitted 事件同一事务(Phase 7a):任务 version 置 1,
	// 事件 sequence=1。eventRepo 与 taskRepo 分属不同表,只有走同一事务才能保证
	// "任务存在 ⇒ submitted 事件存在"。
	CreateWithEvent(task *agent.AgentTask, eventType, source string) error
	MarkReported(id int64) error
	// ListCompletedUnreported 返回该用户在**指定来源渠道**下已完成但未汇报的任务(C 模式核心查询)。
	// 包含 failed 任务--失败也要汇报(08 §9)。
	// source 必填(33 号补强):汇报归属来源渠道,避免 A 渠道的任务被 B 渠道/Web 抢报。
	ListCompletedUnreported(userID int64, source string) ([]*agent.AgentTask, error)
	// ListByUser 列出该用户的**根任务**(按创建时间倒序,limit 限上限)。
	// 34 号:计划子任务不算根任务,任务中心折叠在父任务里。
	ListByUser(userID int64, limit int) ([]*agent.AgentTask, error)
	// CreatePlan 同一事务创建计划容器 + 有序子任务(回填 ParentTaskID)。
	CreatePlan(plan *agent.AgentTask, children []*agent.AgentTask) error
	// ListChildren 列出某计划的子任务(按 StepSeq 升序)。
	ListChildren(parentID int64) ([]*agent.AgentTask, error)
	// UpdateTaskSpec 派发前更新子任务的任务包(注入上一步结论)。goal 同步。
	UpdateTaskSpec(id int64, spec agent.TaskSpec) error
	// ResetForRetry 把失败的子任务重置为 pending(计划续跑重试当前步)。
	ResetForRetry(id int64) error
	// ListRootByStatus 按状态列出根任务(34 号:计划恢复扫 running 容器)。
	ListRootByStatus(status string) ([]*agent.AgentTask, error)
	// UpdateGoal 改 goal(pending 态 update_task 用)。
	UpdateGoal(id int64, goal string) error
	// AppendNote 追加补充信息到 Notes(running 态 update_task 用)。
	AppendNote(id int64, note string) error
}

// GormAgentTaskRepository GORM 实现
type GormAgentTaskRepository struct {
	db *gorm.DB
}

// NewAgentTaskRepository 创建仓储
func NewAgentTaskRepository(db *gorm.DB) AgentTaskRepository {
	return &GormAgentTaskRepository{db: db}
}

func (r *GormAgentTaskRepository) Create(task *agent.AgentTask) error {
	return r.db.Create(task).Error
}

func (r *GormAgentTaskRepository) GetByID(id int64) (*agent.AgentTask, error) {
	var t agent.AgentTask
	err := r.db.Where("id = ?", id).First(&t).Error
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// transitionUpdates 构造状态迁移的 SET 子句(不含 version):目标状态 + 附加字段 + 时间戳。
// 时间戳随目标状态:running→started_at,completed/failed→completed_at,cancelled→cancelled_at。
func transitionUpdates(to string, artifact, errorMsg *string) map[string]interface{} {
	updates := map[string]interface{}{
		"status": to,
	}
	if artifact != nil {
		updates["artifact"] = *artifact
	}
	if errorMsg != nil {
		updates["error_msg"] = *errorMsg
	}
	switch to {
	case agent.TaskStatusCompleted, agent.TaskStatusFailed:
		updates["completed_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	case agent.TaskStatusRunning:
		updates["started_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	case agent.TaskStatusCancelled:
		updates["cancelled_at"] = gorm.Expr("CURRENT_TIMESTAMP")
	}
	return updates
}

// TransitionStatus CAS 状态迁移(Phase 5)。UPDATE ... WHERE id=? AND status=from,
// RowsAffected==0 → 返回 false。先过 CanTransition 状态机表(非法迁移 false, nil)。
// version 随迁移 +1(= 状态变更次数;无事件路径序号允许跳号)。供无事件场景(审计适配器等)。
func (r *GormAgentTaskRepository) TransitionStatus(id int64, from, to string, artifact *string, errorMsg *string) (bool, error) {
	if !agent.CanTransition(from, to) {
		return false, nil
	}
	updates := transitionUpdates(to, artifact, errorMsg)
	updates["version"] = gorm.Expr("version + 1")
	res := r.db.Model(&agent.AgentTask{}).
		Where("id = ? AND status = ?", id, from).
		Updates(updates)
	return res.RowsAffected == 1, res.Error
}

// TransitionStatusWithEvent CAS 状态迁移 + 事件写入同一事务(Phase 7a,16-路线图 §14)。
// CAS 赢得行锁后事务内读回新 version 作为事件 sequence——同事务内无并发写者,读值可信。
func (r *GormAgentTaskRepository) TransitionStatusWithEvent(id int64, from, to string, artifact *string, errorMsg *string, eventType, source string) (bool, error) {
	if !agent.CanTransition(from, to) {
		return false, nil
	}
	ok := false
	err := r.db.Transaction(func(tx *gorm.DB) error {
		updates := transitionUpdates(to, artifact, errorMsg)
		updates["version"] = gorm.Expr("version + 1")
		res := tx.Model(&agent.AgentTask{}).
			Where("id = ? AND status = ?", id, from).
			Updates(updates)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 { // CAS 失败:事务结束(无改动),不写事件
			return nil
		}
		var t agent.AgentTask
		if err := tx.Select("version").Where("id = ?", id).First(&t).Error; err != nil {
			return err
		}
		ev := agent.NewTaskEvent(id, eventType, int(t.Version), source)
		if err := tx.Create(&ev).Error; err != nil {
			return err
		}
		ok = true
		return nil
	})
	return ok, err
}

// CreateWithEvent 建任务 + submitted 事件同一事务(Phase 7a):version=1,事件 sequence=1。
func (r *GormAgentTaskRepository) CreateWithEvent(task *agent.AgentTask, eventType, source string) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		task.Version = 1
		if err := tx.Create(task).Error; err != nil {
			return err
		}
		ev := agent.NewTaskEvent(task.ID, eventType, 1, source)
		return tx.Create(&ev).Error
	})
}

func (r *GormAgentTaskRepository) MarkReported(id int64) error {
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Update("reported", true).Error
}

func (r *GormAgentTaskRepository) ListCompletedUnreported(userID int64, source string) ([]*agent.AgentTask, error) {
	var tasks []*agent.AgentTask
	// parent_task_id IS NULL:34 号合流后,计划子任务也在这张表里,但只有树根才汇报。
	err := r.db.Where("user_id = ? AND source = ? AND parent_task_id IS NULL AND reported = ? AND status IN ?",
		userID, source, false, []string{agent.TaskStatusCompleted, agent.TaskStatusFailed}).
		Order("completed_at ASC").
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

func (r *GormAgentTaskRepository) ListByUser(userID int64, limit int) ([]*agent.AgentTask, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	var tasks []*agent.AgentTask
	err := r.db.Where("user_id = ? AND parent_task_id IS NULL", userID).
		Order("created_at DESC").
		Limit(limit).
		Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// CreatePlan 同一事务创建容器 + 子任务:P nil 时子任务都挂到它下面。
// 一条事务保证"计划存在 ⇒ 步骤齐全",避免半截计划(有容器没步骤)推进时卡死。
func (r *GormAgentTaskRepository) CreatePlan(plan *agent.AgentTask, children []*agent.AgentTask) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(plan).Error; err != nil {
			return err
		}
		parentID := plan.ID
		for _, c := range children {
			c.ParentTaskID = &parentID
			if err := tx.Create(c).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ListChildren 列出计划子任务(按 StepSeq 升序,顺序即执行顺序)。
func (r *GormAgentTaskRepository) ListChildren(parentID int64) ([]*agent.AgentTask, error) {
	var list []*agent.AgentTask
	err := r.db.Where("parent_task_id = ?", parentID).
		Order("step_seq ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ListRootByStatus 按状态列出根任务(不含计划子任务)。
func (r *GormAgentTaskRepository) ListRootByStatus(status string) ([]*agent.AgentTask, error) {
	var list []*agent.AgentTask
	err := r.db.Where("status = ? AND parent_task_id IS NULL", status).
		Order("id ASC").
		Find(&list).Error
	if err != nil {
		return nil, err
	}
	return list, nil
}

// ResetForRetry 失败子任务 → pending(续跑)。只对 failed 生效,其他状态不动。
func (r *GormAgentTaskRepository) ResetForRetry(id int64) error {
	return r.db.Model(&agent.AgentTask{}).
		Where("id = ? AND status = ?", id, agent.TaskStatusFailed).
		Updates(map[string]interface{}{
			"status":     agent.TaskStatusPending,
			"error_msg":  nil,
			"started_at": nil,
			"attempts":   gorm.Expr("attempts + 1"),
		}).Error
}

// UpdateTaskSpec 更新任务包与 goal(派发前注入上一步结论)。
// 结构体 Updates 才会触发 serializer:json,map 更新会写坏 JSON 列。
func (r *GormAgentTaskRepository) UpdateTaskSpec(id int64, spec agent.TaskSpec) error {
	patch := agent.AgentTask{TaskSpec: spec, Goal: spec.Goal}
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Select("task_spec", "goal").
		Updates(&patch).Error
}

// UpdateGoal 改 goal(pending 态 update_task 用)。
func (r *GormAgentTaskRepository) UpdateGoal(id int64, goal string) error {
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Update("goal", goal).Error
}

// AppendNote 追加补充信息到 Notes。Notes 是 JSON 序列化字段(serializer:json),
// 单列 Update/Updates(map) 不走 serializer,需读出现有值手动 append,再用结构体 Updates 写回
// (结构体字段才触发 serializer:json 正确 marshal 为 JSON 字符串)。
func (r *GormAgentTaskRepository) AppendNote(id int64, note string) error {
	var t agent.AgentTask
	if err := r.db.Where("id = ?", id).First(&t).Error; err != nil {
		return err
	}
	t.Notes = append(t.Notes, note)
	// 只更新 notes 列:Select 限定,走 serializer 正确 marshal。
	return r.db.Model(&agent.AgentTask{}).Where("id = ?", id).
		Select("notes").Updates(t).Error
}
