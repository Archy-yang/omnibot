package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"
)

type fakeSkillSaver struct {
	gotInput  *skillsvc.CreateInput
	returned  *skilldomain.Skill
	err       error
}

func (f *fakeSkillSaver) SaveSkillByAgent(in skillsvc.CreateInput) (*skilldomain.Skill, error) {
	f.gotInput = &in
	if f.err != nil {
		return nil, f.err
	}
	return f.returned, nil
}

func okSaveArgs() map[string]interface{} {
	return map[string]interface{}{
		"name":        "charge-analysis",
		"description": "分析飞书充电记录表的电耗与成本",
		"instruction": "## 步骤\n1. 用 feishu 读《充电记录》表",
		"files": []interface{}{
			map[string]interface{}{"path": "analyze.py", "content": "print(1)"},
		},
	}
}

// T1 定义:name/必填参数/描述含"默认停用待审"语义。
func TestSaveSkillTool_Definition(t *testing.T) {
	tool := CreateSaveSkillTool(&fakeSkillSaver{})
	assert.Equal(t, "save_skill", tool.Name)
	assert.NotEmpty(t, tool.DisplayLabel)
	props := tool.Parameters["properties"].(map[string]interface{})
	for _, k := range []string{"name", "display_name", "description", "instruction", "files"} {
		assert.Contains(t, props, k)
	}
	assert.True(t, strings.Contains(tool.Description, "停用") || strings.Contains(tool.Description, "审阅"),
		"描述应写明默认停用待审语义")
}

// T2 成功流:参数转 CreateInput 透传;返回文案含"审阅/开启"指引。
func TestSaveSkillTool_Success(t *testing.T) {
	saver := &fakeSkillSaver{returned: &skilldomain.Skill{ID: 1, Name: "charge-analysis"}}
	tool := CreateSaveSkillTool(saver)
	out, err := tool.Execute(context.Background(), okSaveArgs())
	require.NoError(t, err)
	require.NotNil(t, saver.gotInput)
	assert.Equal(t, "charge-analysis", saver.gotInput.Name)
	assert.Equal(t, "分析飞书充电记录表的电耗与成本", saver.gotInput.Description)
	require.Len(t, saver.gotInput.Files, 1)
	assert.Equal(t, "analyze.py", saver.gotInput.Files[0].Path)
	assert.Contains(t, out, "停用")
	assert.Contains(t, out, "审阅")
}

// T3 必填校验:name/description/instruction 缺一拒绝,不触达 service。
func TestSaveSkillTool_RequiresFields(t *testing.T) {
	saver := &fakeSkillSaver{}
	tool := CreateSaveSkillTool(saver)

	missing := okSaveArgs()
	delete(missing, "name")
	_, err := tool.Execute(context.Background(), missing)
	require.Error(t, err)
	assert.Nil(t, saver.gotInput)

	missing = okSaveArgs()
	delete(missing, "description")
	_, err = tool.Execute(context.Background(), missing)
	require.Error(t, err)

	missing = okSaveArgs()
	delete(missing, "instruction")
	_, err = tool.Execute(context.Background(), missing)
	require.Error(t, err)
}

// T4 重名 → 可读错误(换名指引);service 其他错误透传。
func TestSaveSkillTool_Errors(t *testing.T) {
	tool := CreateSaveSkillTool(&fakeSkillSaver{err: skillsvc.ErrNameTaken})
	_, err := tool.Execute(context.Background(), okSaveArgs())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "已存在") && strings.Contains(err.Error(), "换"),
		"重名错误应指引换 name: %v", err)

	tool2 := CreateSaveSkillTool(&fakeSkillSaver{err: skillsvc.ErrTooLarge})
	_, err = tool2.Execute(context.Background(), okSaveArgs())
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "上限") || strings.Contains(err.Error(), "字节"))
}
