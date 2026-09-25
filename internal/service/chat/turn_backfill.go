package chat

import (
	"fmt"
	"time"

	"omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"

	"gorm.io/gorm"
)

// TurnBackfillStats 回填结果统计。
type TurnBackfillStats struct {
	Conversations int // 新建的 conversation 数
	Turns         int // 新建的 turn 数
	Messages      int // 补上 turn_id/conversation_id 的消息数
	ReportsLinked int // 按任务归属回填的 report 消息数
	TasksLinked   int // 补上 origin_turn_id 的任务数
}

// BackfillConversationTurns 存量消息 Turn 回填(Phase 1-4,16-架构迭代路线图 §5.2)。
//
// Turn 可以从时间线近似重建:
//  1. 第一遍:按 id 正序扫普通消息,每条 user message 开新 Turn,其后的 assistant 归属它;
//  2. 第二遍:report 消息经 task_id → agent_tasks,取"任务发起时所在的 Turn"
//     (即 created_at 早于 task.CreatedAt 的最近一条 user message 的 Turn),
//     并把该 Turn 同步写回 agent_tasks.origin_turn_id。
//
// 幂等:已带 turn_id 的消息跳过;重复执行不产生新 Turn/Conversation。
// 近似误差只存在于"task 创建后很久才汇报且中间夹了新对话"的场景,可接受。
func BackfillConversationTurns(db *gorm.DB) (*TurnBackfillStats, error) {
	stats := &TurnBackfillStats{}

	// main agent(种子通常已由 AutoMigrate 写入;防御性补建)
	var ag agent.Agent
	if err := db.Where("code = ?", agent.AgentCodeMain).First(&ag).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			return nil, fmt.Errorf("query main agent: %w", err)
		}
		ag = agent.Agent{Code: agent.AgentCodeMain, Name: "OmniBot 主助理"}
		if err := db.Create(&ag).Error; err != nil {
			return nil, fmt.Errorf("seed main agent: %w", err)
		}
		stats.Conversations++ // 统计口径:种子算一次初始化动作,便于日志观察
	}

	// 按用户分组处理(用户间互不影响)
	var userIDs []int64
	if err := db.Model(&conversation.Message{}).Distinct().Pluck("user_id", &userIDs).Error; err != nil {
		return nil, fmt.Errorf("list message users: %w", err)
	}

	for _, userID := range userIDs {
		if err := backfillUser(db, userID, ag.ID, stats); err != nil {
			return nil, fmt.Errorf("backfill user %d: %w", userID, err)
		}
	}
	return stats, nil
}

// backfillUser 回填单个用户的消息归属。
func backfillUser(db *gorm.DB, userID, agentID int64, stats *TurnBackfillStats) error {
	var msgs []conversation.Message
	if err := db.Where("user_id = ?", userID).Order("id ASC").Find(&msgs).Error; err != nil {
		return fmt.Errorf("list messages: %w", err)
	}
	if len(msgs) == 0 {
		return nil
	}

	// 已全部回填过则直接跳过(幂等)
	needBackfill := false
	for i := range msgs {
		if msgs[i].TurnID == nil {
			needBackfill = true
			break
		}
	}
	if !needBackfill {
		return nil
	}

	// ensure conversation
	var conv conversation.Conversation
	err := db.Where("user_id = ? AND agent_id = ? AND status = ?",
		userID, agentID, conversation.ConversationStatusActive).First(&conv).Error
	if err != nil {
		if err != gorm.ErrRecordNotFound {
			return fmt.Errorf("query conversation: %w", err)
		}
		conv = *conversation.NewConversation(userID, agentID)
		if err := db.Create(&conv).Error; err != nil {
			return fmt.Errorf("create conversation: %w", err)
		}
		stats.Conversations++
	}

	// 第一遍:普通消息按 user message 边界切 Turn
	// turnStarts 记录每个 Turn 的起始时间,供第二遍按 task.CreatedAt 定位
	var currentTurnID int64
	hasTurn := false
	turnByMsg := map[int64]int64{}   // msgID -> turnID
	turnStartTime := map[int64]time.Time{}
	pendingReports := []conversation.Message{}

	for i := range msgs {
		msg := &msgs[i]
		isReport := msg.Kind == conversation.KindReport
		if !isReport && msg.Role == conversation.RoleUser {
			turn := conversation.NewConversationTurn(conv.ID, userID)
			if err := db.Create(turn).Error; err != nil {
				return fmt.Errorf("create turn: %w", err)
			}
			stats.Turns++
			currentTurnID = turn.ID
			hasTurn = true
			turnStartTime[turn.ID] = msg.CreatedAt
		}
		if !hasTurn {
			// 第一条消息不是 user(异常数据):留空,与"无 Turn 上下文"语义一致
			continue
		}
		if isReport {
			// 第二遍处理:需查任务
			pendingReports = append(pendingReports, *msg)
			continue
		}
		turnByMsg[msg.ID] = currentTurnID
	}

	// 批量落库:conversation_id + turn_id
	if err := applyTurnAssignments(db, conv.ID, turnByMsg); err != nil {
		return err
	}
	stats.Messages += len(turnByMsg)

	// 第二遍:report 按 task 定位发起时所在 Turn
	for _, report := range pendingReports {
		turnID, found, err := resolveReportTurn(db, report, turnStartTime)
		if err != nil {
			return err
		}
		if !found {
			continue // 任务缺失/无法定位:留空
		}
		if err := db.Model(&conversation.Message{}).Where("id = ?", report.ID).
			Updates(map[string]interface{}{"conversation_id": conv.ID, "turn_id": turnID}).Error; err != nil {
			return fmt.Errorf("assign report turn: %w", err)
		}
		stats.Messages++
		stats.ReportsLinked++
		if report.TaskID != nil {
			if err := db.Model(&agent.AgentTask{}).Where("id = ? AND origin_turn_id IS NULL", *report.TaskID).
				Update("origin_turn_id", turnID).Error; err != nil {
				return fmt.Errorf("backfill task origin turn: %w", err)
			}
			stats.TasksLinked++
		}
	}
	return nil
}

// resolveReportTurn 定位 report 的逻辑归属 Turn:任务发起时(created_at 早于 task.CreatedAt)
// 最近一条 user message 的 Turn。task.origin_turn_id 已有值时直接采用。
func resolveReportTurn(db *gorm.DB, report conversation.Message, turnStartTime map[int64]time.Time) (int64, bool, error) {
	if report.TaskID == nil {
		return 0, false, nil
	}
	var task agent.AgentTask
	if err := db.Select("id", "origin_turn_id", "created_at").First(&task, *report.TaskID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return 0, false, nil // 任务已被清理:留空
		}
		return 0, false, fmt.Errorf("query task %d: %w", *report.TaskID, err)
	}
	if task.OriginTurnID != nil {
		return *task.OriginTurnID, true, nil
	}
	// 时间近似:找起始时间早于 task.CreatedAt 的最近 Turn
	var bestID int64
	var bestTime time.Time
	for turnID, start := range turnStartTime {
		if !start.After(task.CreatedAt) && start.After(bestTime) {
			bestTime = start
			bestID = turnID
		}
	}
	if bestID == 0 {
		return 0, false, nil
	}
	return bestID, true, nil
}

// applyTurnAssignments 批量写 conversation_id/turn_id(按 Turn 分组,一次 UPDATE 多条)。
func applyTurnAssignments(db *gorm.DB, conversationID int64, turnByMsg map[int64]int64) error {
	byTurn := map[int64][]int64{}
	for msgID, turnID := range turnByMsg {
		byTurn[turnID] = append(byTurn[turnID], msgID)
	}
	for turnID, msgIDs := range byTurn {
		if err := db.Model(&conversation.Message{}).Where("id IN ?", msgIDs).
			Updates(map[string]interface{}{"conversation_id": conversationID, "turn_id": turnID}).Error; err != nil {
			return fmt.Errorf("assign turns: %w", err)
		}
	}
	return nil
}
