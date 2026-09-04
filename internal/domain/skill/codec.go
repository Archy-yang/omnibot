package skill

import (
	"encoding/json"
	"strings"
)

// joinCapabilities 能力标签列表 → 逗号分隔串(空列表返回空串)。
func JoinCapabilities(caps []string) string {
	return strings.Join(caps, ",")
}

// marshalSchema JSON Schema → 字符串(序列化失败返回空串,视为无参工具)。
func MarshalSchema(params map[string]interface{}) string {
	if len(params) == 0 {
		return ""
	}
	b, err := json.Marshal(params)
	if err != nil {
		return ""
	}
	return string(b)
}

// SplitCapabilities 逗号分隔串 → 能力标签列表(公开供 service 还原)。
func SplitCapabilities(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	caps := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			caps = append(caps, p)
		}
	}
	return caps
}

// UnmarshalSchema JSON 字符串 → JSON Schema map(非法或空返回 false,调用方视为 schema 不可用)。
func UnmarshalSchema(s string) (map[string]interface{}, bool) {
	if s == "" {
		return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}, true
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return nil, false
	}
	return m, true
}
