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
