package chat

import (
	"testing"
	"time"

	"omnibot/internal/db"
	agentdomain "omnibot/internal/domain/agent"
	"omnibot/internal/domain/conversation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBackfillConversationTurns 存量消息 Turn 回填(Phase 1-4,16-架构迭代路线图 §5.2):
// 第一遍按 user message 边界切 Turn;第二遍 report 经 task 定位"任务发起时所在的 Turn"。
func TestBackfillConversationTurns(t *testing.T) {
	testDB := db.NewTestDB(t)
	require.NoError(t, testDB.AutoMigrate(&agentdomain.Agent{}, &agentdomain.AgentTask{}))

	base := time.Now().Add(-time.Hour)
	t0 := func(offset int) time.Time { return base.Add(time.Duration(offset) * time.Minute) }
	insertMsg := func(role, content string, offset int, kind string, taskID *int64) *conversation.Message {
		msg := &conversation.Message{UserID: 42, Role: role, Content: content, Kind: kind, TaskID: taskID, CreatedAt: t0(offset)}
		require.NoError(t, testDB.Create(msg).Error)
		return msg
	}

	// 存量时间线(无任何 Turn):两个完整轮 + 一个任务 + 期间两轮 + 迟到的汇报
	m1 := insertMsg(conversation.RoleUser, "帮我调研 X", 0, "", nil)                 // Turn A 开启
	m2 := insertMsg(conversation.RoleAssistant, "已安排", 1, "", nil)
	m3 := insertMsg(conversation.RoleUser, "期间的问题1", 2, "", nil)                  // Turn B 开启
	insertMsg(conversation.RoleAssistant, "回复1", 3, "", nil)
	// 任务 888 在 Turn B 期间创建(created_at 介于 m4 与 m5 之间)
	created := t0(4)
	task := &agentdomain.AgentTask{UserID: 42, Goal: "调研 X", Status: "running", SubAgentType: "", CreatedAt: created}
	require.NoError(t, testDB.Create(task).Error)
	m5 := insertMsg(conversation.RoleUser, "期间的问题2", 5, "", nil)                  // Turn C 开启
	insertMsg(conversation.RoleAssistant, "回复2", 6, "", nil)
	m7 := insertMsg(conversation.RoleAssistant, "调研完成……", 7, conversation.KindReport, &task.ID) // 迟到的汇报

	// 另一个用户一条消息:独立 conversation
	m8 := insertMsg(conversation.RoleUser, "你好", 1, "", nil)
	m8.UserID = 43
	require.NoError(t, testDB.Save(m8).Error)

	stats, err := BackfillConversationTurns(testDB)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Conversations)
	assert.Equal(t, 4, stats.Turns) // user42: A/B/C 三条;user43: 一条
	assert.Equal(t, 8, stats.Messages)
	assert.Equal(t, 1, stats.ReportsLinked)

	// 普通消息:按 user message 边界归属
	var m1r, m2r, m3r, m5r, m7r conversation.Message
	require.NoError(t, testDB.First(&m1r, m1.ID).Error)
	require.NoError(t, testDB.First(&m2r, m2.ID).Error)
	require.NoError(t, testDB.First(&m3r, m3.ID).Error)
	require.NoError(t, testDB.First(&m5r, m5.ID).Error)
	require.NoError(t, testDB.First(&m7r, m7.ID).Error)

	require.NotNil(t, m1r.TurnID)
	assert.Equal(t, *m1r.TurnID, *m2r.TurnID, "q1 的回复归属同一 Turn")
	assert.NotNil(t, m3r.TurnID)
	assert.NotEqual(t, *m1r.TurnID, *m3r.TurnID, "新 user message 开启新 Turn")
	assert.NotEqual(t, *m3r.TurnID, *m5r.TurnID)

	// 汇报逻辑归属:task 创建于 Turn B → m7.turn_id = Turn B(即使时间线在 Turn C 之后)
	require.NotNil(t, m7r.TurnID)
	assert.Equal(t, *m3r.TurnID, *m7r.TurnID, "report 应归属任务发起时的 Turn")

	// agent_tasks.origin_turn_id 同步回填
	var taskRow agentdomain.AgentTask
	require.NoError(t, testDB.First(&taskRow, task.ID).Error)
	require.NotNil(t, taskRow.OriginTurnID)
	assert.Equal(t, *m3r.TurnID, *taskRow.OriginTurnID)

	// 用户 43 独立 conversation
	var m8r conversation.Message
	require.NoError(t, testDB.First(&m8r, m8.ID).Error)
	require.NotNil(t, m8r.ConversationID)
	require.NotEqual(t, *m1r.ConversationID, *m8r.ConversationID)
}

// TestBackfillConversationTurns_Idempotent 重复执行不产生新 Turn/新 Conversation。
func TestBackfillConversationTurns_Idempotent(t *testing.T) {
	testDB := db.NewTestDB(t)
	require.NoError(t, testDB.AutoMigrate(&agentdomain.Agent{}, &agentdomain.AgentTask{}))

	msg := &conversation.Message{UserID: 42, Role: conversation.RoleUser, Content: "q", CreatedAt: time.Now()}
	require.NoError(t, testDB.Create(msg).Error)

	_, err := BackfillConversationTurns(testDB)
	require.NoError(t, err)
	var turnCount int64
	testDB.Model(&conversation.ConversationTurn{}).Count(&turnCount)
	first := turnCount

	_, err = BackfillConversationTurns(testDB)
	require.NoError(t, err)
	testDB.Model(&conversation.ConversationTurn{}).Count(&turnCount)
	assert.Equal(t, first, turnCount, "重复回填不应新建 Turn")
}
