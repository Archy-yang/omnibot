package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseDelegateTaskID_Valid(t *testing.T) {
	got := parseDelegateTaskID(`{"task_id": 43, "status": "pending", "message": "已安排"}`)
	assert.Equal(t, int64(43), got)
}

func TestParseDelegateTaskID_Invalid(t *testing.T) {
	assert.Equal(t, int64(0), parseDelegateTaskID("not json"))
	assert.Equal(t, int64(0), parseDelegateTaskID(`{"status": "pending"}`)) // 无 task_id
	assert.Equal(t, int64(0), parseDelegateTaskID(""))
}

func TestAppendTaskIDs_WithIDs(t *testing.T) {
	got := appendTaskIDs("已安排研究员查高铁", []int64{43})
	assert.Equal(t, "已安排研究员查高铁\n\n---\n任务ID: 43", got)
}

func TestAppendTaskIDs_MultipleIDs(t *testing.T) {
	got := appendTaskIDs("已安排", []int64{43, 44})
	assert.Equal(t, "已安排\n\n---\n任务ID: 43, 44", got)
}

func TestAppendTaskIDs_NoIDs(t *testing.T) {
	// 无 task_id 原样返回(没调 delegate 的回复不拼接)
	got := appendTaskIDs("今天天气不错", nil)
	assert.Equal(t, "今天天气不错", got)
	assert.NotContains(t, got, "任务ID")
}
