package agent

import (
	"encoding/json"
	"time"
)

// Artifact 子 Agent 的结构化产出(替代自由 text)。
//
// 设计目的(见 10-多Agent通讯协议演进规划 §2.3):子 Agent 完成后不再只返回一坨自由文本,
// 而是结构化产物(name/content_type/schema/content)。主 Agent 按 schema 字段取用,
// 不再解析自由文本。兼容老 artifact(task.Artifact text 列)。
//
// 存独立 agent_artifacts 表。task.Artifact 保留作向后兼容(存文本摘要或指向 artifact)。
type Artifact struct {
	ID          int64           `json:"id" gorm:"primaryKey;autoIncrement"`
	TaskID      int64           `json:"task_id" gorm:"index;not null"`
	Name        string          `json:"name" gorm:"size:100"`             // 产物名,如 "research_report"
	ContentType string          `json:"content_type" gorm:"size:100"`     // "text/markdown" / "application/json"
	SchemaName  string          `json:"schema_name,omitempty" gorm:"size:100"` // 如 "agent.research-report.v1"
	Content     json.RawMessage `json:"content" gorm:"type:text"`         // 结构化内容(JSON)
	CreatedAt   time.Time       `json:"created_at" gorm:"not null"`
}

// TableName 指定表名
func (Artifact) TableName() string {
	return "agent_artifacts"
}

// 常用 content_type
const (
	ArtifactContentTypeMarkdown = "text/markdown"
	ArtifactContentTypeJSON     = "application/json"
)

// NewMarkdownArtifact 构造一个 markdown 文本 artifact(content={"text": "..."}),
// 兼容子 Agent 仍产出自由文本的场景(不强制改 prompt)。
func NewMarkdownArtifact(taskID int64, name, text string) *Artifact {
	content, _ := json.Marshal(map[string]string{"text": text})
	return &Artifact{
		TaskID:      taskID,
		Name:        name,
		ContentType: ArtifactContentTypeMarkdown,
		Content:     content,
		CreatedAt:   time.Now(),
	}
}

// Text 提取 markdown artifact 的文本内容(兼容老 artifact 的自由文本读取)。
// 非 markdown 或解析失败返回空串。
func (a *Artifact) Text() string {
	if a == nil || len(a.Content) == 0 {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal(a.Content, &m); err != nil {
		return ""
	}
	return m["text"]
}
