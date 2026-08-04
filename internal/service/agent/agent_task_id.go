package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

// parseDelegateTaskID 从 delegate 工具返回的 JSON 提取 task_id。
// delegate Execute 返回 {"task_id": 43, "status": "pending", ...}。
// 解析失败(非 JSON/无 task_id)返回 0,安全跳过(不阻断流程)。
func parseDelegateTaskID(toolResult string) int64 {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(toolResult), &m); err != nil {
		return 0
	}
	// task_id 在 JSON 里可能是 float64(json.Unmarshal 默认数字类型)
	switch v := m["task_id"].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// appendTaskIDs 在回复末尾追加任务标识。无 task_id 原样返回(不拼接)。
// 格式:
//
//	<回复正文>
//
//	---
//	任务ID: 43
//
// 多个 task:
//
//	---
//	任务ID: 43, 44
func appendTaskIDs(content string, taskIDs []int64) string {
	if len(taskIDs) == 0 {
		return content
	}
	parts := make([]string, 0, len(taskIDs))
	for _, id := range taskIDs {
		parts = append(parts, fmt.Sprintf("%d", id))
	}
	return content + "\n\n---\n任务ID: " + strings.Join(parts, ", ")
}
