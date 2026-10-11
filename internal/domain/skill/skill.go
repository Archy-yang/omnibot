// Package skill 定义技能域(Skill)的持久化模型。
// 18-Skill技能系统技术方案 §3:Skill = 用户可定义的、按需注入的、可带脚本的程序性知识包。
// DB 为单一事实源(在线编辑/在线添加/Agent 自动生成都要求落库,对齐 mcp_servers M3 先例)。
package skill

import "time"

// 来源常量(18 号 §3)。
const (
	SourceManual = "manual" // 用户在 Web 端手写
	SourceAgent  = "agent"  // Agent 经 save_skill 生成(默认 enabled=false 待审)
)

// Skill 技能主表。name 为目录 key(创建后不可变);description 进技能目录(常驻注入的
// 匹配依据);instruction 为指令正文(按需加载,≤32KB)。
type Skill struct {
	ID          int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	Name        string    `gorm:"uniqueIndex;size:64;not null" json:"name"` // 目录 key,^[a-z0-9-]{2,64}$,创建后不可变
	DisplayName string    `gorm:"size:64" json:"display_name"`              // 面向用户的中文名
	Description string    `gorm:"size:512;not null" json:"description"`     // 一句话概述(目录注入,写清"何时用")
	Instruction string    `gorm:"type:text" json:"instruction"`             // 指令正文(Markdown,≤32KB)
	Source      string    `gorm:"size:16;not null;default:manual" json:"source"`
	Enabled     bool      `gorm:"not null" json:"enabled"`
	UserID      *int64    `gorm:"index" json:"user_id,omitempty"` // NULL=共享(单用户期恒 NULL,预留按人隔离)
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt   time.Time `gorm:"not null" json:"updated_at"`
}

func (Skill) TableName() string { return "skills" }

// SkillFile 技能附属文件(脚本/资源)。content 为文本,传 sandbox_run 的 Files 执行(17 号)。
type SkillFile struct {
	ID        int64     `gorm:"primaryKey;autoIncrement" json:"id"`
	SkillID   int64     `gorm:"uniqueIndex:idx_skill_file_path;not null" json:"skill_id"`
	Path      string    `gorm:"size:256;uniqueIndex:idx_skill_file_path;not null" json:"path"` // 相对路径,如 analyze.py
	Content   string    `gorm:"type:text" json:"content"`                                      // ≤64KB/文件
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

func (SkillFile) TableName() string { return "skill_files" }

// 大小/数量上限(18 号 §3 约束——防目录注入与按需加载被单技能撑爆)。
const (
	MaxInstructionBytes = 32 * 1024
	MaxDescriptionBytes = 512
	MaxFileBytes        = 64 * 1024
	MaxFiles            = 10
)
