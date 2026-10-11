package tools

import (
	"context"
	"errors"
	"fmt"

	"omnibot/internal/pkg/toolcore"
	skillsvc "omnibot/internal/service/skill"
)

// mapLoadErr 加载错误转可读文案(技能不存在/未启用是常见路径,须可自纠)。
func mapLoadErr(err error) error {
	if errors.Is(err, skillsvc.ErrNotFound) {
		return fmt.Errorf("技能不存在或未启用(name 须来自技能目录;未启用的技能请用户在设置页开启)")
	}
	return err
}

// SkillLoader load_skill 所需的窄接口(service/skill.SkillService 天然满足)。
// 技能是主 Agent 的"方法库"(18-Skill技能系统技术方案 §5.2):目录常驻注入,
// 正文按需加载;子 Agent 不可见(执行统一走 delegate)。
type SkillLoader interface {
	LoadInstruction(name string) (string, []skillsvc.FileMeta, error)
	LoadFile(name, path string) (string, error)
}

// CreateLoadSkillTool 构造 load_skill 工具(18 号 §5.2)。
func CreateLoadSkillTool(loader SkillLoader) toolcore.Tool {
	return toolcore.Tool{
		Name:         "load_skill",
		DisplayLabel: "加载了技能",
		Description: "加载某个技能的详细做法。当用户请求命中技能目录中的某项时调用," +
			"返回该技能的操作指令(何时读什么数据、怎么算、按什么口径汇报)。" +
			"默认返回指令正文与附属文件清单;指令要求执行脚本时,再带 file 参数取脚本内容," +
			"并把脚本作为 files 传给 sandbox_run 执行。",
		Capabilities: []string{toolcore.CapBasic},
		Parameters: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type":        "string",
					"description": "技能 name(见系统提示中的技能目录)",
				},
				"file": map[string]interface{}{
					"type":        "string",
					"description": "可选。要取内容的附属文件相对路径(如 analyze.py);不带则返回指令正文+文件清单",
				},
			},
			"required": []string{"name"},
		},
		Execute: func(ctx context.Context, args map[string]interface{}) (string, error) {
			name, _ := args["name"].(string)
			if name == "" {
				return "", fmt.Errorf("name 必填(技能目录中列出的 name)")
			}
			if file, ok := args["file"].(string); ok && file != "" {
				content, err := loader.LoadFile(name, file)
				if err != nil {
					return "", mapLoadErr(err)
				}
				return content, nil
			}
			instruction, files, err := loader.LoadInstruction(name)
			if err != nil {
				return "", mapLoadErr(err)
			}
			out := instruction
			if len(files) > 0 {
				out += "\n\n--- 附属文件 ---\n"
				for _, f := range files {
					out += "- " + f.Path + "(带 file 参数取内容,经 sandbox_run 的 files 执行)\n"
				}
			}
			return out, nil
		},
	}
}
