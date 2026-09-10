package agent

import (
	"fmt"

	domainagent "omnibot/internal/domain/agent"
	repoagent "omnibot/internal/repository/agent"
)

// NoteInjectionHook 把 update_task 追加的 notes 注入子 Agent 上下文(running 态补充信息)。
//
// 场景:子 Agent 已派出在跑,用户补充信息(如"顺便也查 X")。update_task 把 note 追加到 task.Notes,
// 本 hook 在每轮 BeforeRound 检查:若有新 notes(超过已注入索引),逐条追加 user 消息到 rt.Messages,
// 让模型本轮读到并入推理。已注入的不重复。
//
// 仅 BeforeRound 切点生效(其他放行)。子 Agent runner 装配(主 Agent 不需要)。
type NoteInjectionHook struct {
	taskID   int64
	taskRepo repoagent.AgentTaskRepository // 可 nil(测试用 setNotesForTest 替代)
	// injectedCount 已注入的 notes 数(避免重复注入)。下次从 notes[injectedCount:] 开始。
	injectedCount int
	// testNotes 测试注入(非空时优先用,不查 taskRepo)
	testNotes []string
}

// NewNoteInjectionHook 创建 notes 注入 hook。
func NewNoteInjectionHook(taskID int64, taskRepo repoagent.AgentTaskRepository) *NoteInjectionHook {
	return &NoteInjectionHook{taskID: taskID, taskRepo: taskRepo}
}

// BeforeRound 检查 task.Notes,新 notes 追加 user 消息到 rt.Messages。
// 实现 RoundHook 接口。
func (h *NoteInjectionHook) BeforeRound(rt *Runtime) []map[string]interface{} {
	notes := h.currentNotes()
	newNotes := notes[h.injectedCount:]
	for _, n := range newNotes {
		rt.Messages = append(rt.Messages, map[string]interface{}{
			"role":    "user",
			"content": fmt.Sprintf("[补充信息] %s", n),
		})
	}
	h.injectedCount = len(notes)
	return rt.Tools // 不改 tools
}

// OnLLMResult 放行(不干预)。
func (h *NoteInjectionHook) OnLLMResult(rt *Runtime, _ string, _ string, _ []ToolCall) bool {
	return true
}

// OnToolExecute 不拦截。
func (h *NoteInjectionHook) OnToolExecute(rt *Runtime, _ ToolCall) (string, string, bool) {
	return "", "", false
}

// OnMaxExhausted 不产出(交 ForceSummaryHook)。
func (h *NoteInjectionHook) OnMaxExhausted(rt *Runtime) string { return "" }

// currentNotes 取当前 task 的 notes。测试用 testNotes;生产用 taskRepo。
func (h *NoteInjectionHook) currentNotes() []string {
	if h.testNotes != nil {
		return h.testNotes
	}
	if h.taskRepo == nil {
		return nil
	}
	task, err := h.taskRepo.GetByID(h.taskID)
	if err != nil || task == nil {
		return nil
	}
	return task.Notes
}

// setNotesForTest 测试辅助:注入 notes(不查 taskRepo)。
func (h *NoteInjectionHook) setNotesForTest(notes []string) {
	h.testNotes = notes
}

var _ = domainagent.TaskStatusPending
