package tools

import (
	"context"
	"errors"
	"fmt"

	skilldomain "omnibot/internal/domain/skill"
	"omnibot/internal/pkg/toolcore"
	skillsvc "omnibot/internal/service/skill"
)

// SkillSaver save_skill 所需的窄接口(service/skill.SkillService 天然满足)。
type SkillSaver interface {
	SaveSkillByAgent(in skillsvc.CreateInput) (*skilldomain.Skill, error)
}

// CreateSaveSkillTool 构造 save_skill 工具(18 号 §5.3,S1.5):
// 对话里把流程沉淀为技能——落库 source=agent、enabled=false,用户审阅后 Web 开启。
// 本工具不可修改 enabled(启停只走 Web,防 Agent 未经审阅扩充自己的指令面)。
func CreateSaveSkillTool(saver SkillSaver) toolcore.Tool {
	return toolcore.Tool{
		Name:         "save_skill",
		DisplayLabel: "沉淀了技能",
		Description: "把当前可复用的操作流程沉淀为技能(如用户说'把充电分析做成技能')。" +
			"传入 name(小写字母/数字/连字符)、description(一句话说明何时用)和 instruction" +
			"(Markdown 操作步骤,何时读什么数据、怎么算、按什么口径汇报),可选带脚本文件 files。" +
			"技能默认停用,用户审阅后在设置-技能中开启才会生效;name 已存在时会报错,请换一个 name。",
		Capabilities: []string{toolcore.CapBasic},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "技能 name,小写字母/数字/连字符(如 charge-analysis);已存在时换名,不要覆盖",
				},
				"display_name": map[string]interface{}{
					"type":        "string",
					"description": "可选。面向用户的中文名(如 充电分析)",
				},
				"description": map[string]interface{}{
					"type":        "string",
					"description": "一句话概述:这个技能做什么、什么时候用(目录注入的匹配依据,写清触发场景)",
				},
				"instruction": map[string]interface{}{
					"type":        "string",
					"description": "Markdown 操作指令:分步骤写清做法与汇报口径,可引用现有工具(feishu/web_read/sandbox_run 等)",
				},
				"files": map[string]interface{}{
					"type": "array",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"path":    map[string]interface{}{"type": "string", "description": "相对路径,如 analyze.py"},
							"content": map[string]interface{}{"type": "string", "description": "文件内容(文本)"},
						},
						"required": []string{"path", "content"},
					},
					"description": "可选。脚本/参考文件(≤10 个,单个 ≤64KB)",
				},
			},
			"required": []string{"name", "description", "instruction"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			displayName, _ := args["display_name"].(string)
			description, _ := args["description"].(string)
			instruction, _ := args["instruction"].(string)
			if name == "" || description == "" || instruction == "" {
				return "", fmt.Errorf("name、description、instruction 均必填")
			}
			in := skillsvc.CreateInput{Name: name, DisplayName: displayName, Description: description, Instruction: instruction}
			if rawFiles, ok := args["files"].([]interface{}); ok {
				for _, fv := range rawFiles {
					fm, ok := fv.(map[string]interface{})
					if !ok {
						return "", fmt.Errorf("files 元素必须是对象({path, content})")
					}
					path, _ := fm["path"].(string)
					content, _ := fm["content"].(string)
					in.Files = append(in.Files, skillsvc.FileInput{Path: path, Content: content})
				}
			}
			if _, err := saver.SaveSkillByAgent(in); err != nil {
				return "", mapSaveErr(err)
			}
			return "技能已保存为草稿(默认停用):请告知用户在「设置-技能」中审阅并开启后生效。", nil
		},
	}
}

// mapSaveErr 保存错误转可读文案。
func mapSaveErr(err error) error {
	switch {
	case errors.Is(err, skillsvc.ErrNameTaken):
		return fmt.Errorf("该 name 已存在,请换一个 name(不要覆盖既有技能)")
	case errors.Is(err, skillsvc.ErrNameInvalid):
		return fmt.Errorf("name 格式非法:2~64 位小写字母/数字/连字符")
	default:
		return err
	}
}
