package agent

import (
	"encoding/json"
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
