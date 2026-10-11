package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skillsvc "omnibot/internal/service/skill"
)

// fakeSkillLoader 捕获调用参数,返回预定内容。
type fakeSkillLoader struct {
	gotName string
	gotFile string

	instruction string
	files       []skillsvc.FileMeta
	fileContent string
	err         error
}

func (f *fakeSkillLoader) LoadInstruction(name string) (string, []skillsvc.FileMeta, error) {
	f.gotName = name
	return f.instruction, f.files, f.err
}

func (f *fakeSkillLoader) LoadFile(name, path string) (string, error) {
	f.gotName, f.gotFile = name, path
	return f.fileContent, f.err
}

// T1 无 file 参数:返回指令正文 + 文件清单(提示二段式加载)。
func TestLoadSkillTool_InstructionAndFileList(t *testing.T) {
	loader := &fakeSkillLoader{
		instruction: "## 步骤\n1. 读表 2. 算电耗",
		files:       []skillsvc.FileMeta{{Path: "analyze.py"}},
	}
	tool := CreateLoadSkillTool(loader)
	out, err := tool.Execute(context.Background(), map[string]interface{}{"name": "charge-analysis"})
	require.NoError(t, err)
	assert.Equal(t, "charge-analysis", loader.gotName)
	assert.Contains(t, out, "读表")
	assert.Contains(t, out, "analyze.py")
	assert.Contains(t, out, "file", "应提示带 file 参数取脚本内容")
}

// T2 带 file 参数:返回该文件内容(传 sandbox_run 的 Files 用)。
func TestLoadSkillTool_FileContent(t *testing.T) {
	loader := &fakeSkillLoader{fileContent: "print(1)"}
	tool := CreateLoadSkillTool(loader)
	out, err := tool.Execute(context.Background(), map[string]interface{}{
		"name": "charge-analysis", "file": "analyze.py",
	})
	require.NoError(t, err)
	assert.Equal(t, "analyze.py", loader.gotFile)
	assert.Equal(t, "print(1)", out)
}

// T3 缺 name / 空 name → 可读错误。
func TestLoadSkillTool_RequiresName(t *testing.T) {
	tool := CreateLoadSkillTool(&fakeSkillLoader{})
	_, err := tool.Execute(context.Background(), map[string]interface{}{})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "name"))
}

// T4 未启用/不存在 → 错误原样透传(ErrNotFound 文案可读)。
func TestLoadSkillTool_NotFoundPassthrough(t *testing.T) {
	tool := CreateLoadSkillTool(&fakeSkillLoader{err: skillsvc.ErrNotFound})
	_, err := tool.Execute(context.Background(), map[string]interface{}{"name": "no-such"})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "不存在或未启用"))
}

// T5 定义:主 Agent 可见能力打标、描述写明用途。
func TestLoadSkillTool_Definition(t *testing.T) {
	tool := CreateLoadSkillTool(&fakeSkillLoader{})
	assert.Equal(t, "load_skill", tool.Name)
	assert.NotEmpty(t, tool.DisplayLabel)
	assert.NotEmpty(t, tool.Description)
	props := tool.Parameters["properties"].(map[string]interface{})
	assert.Contains(t, props, "name")
	assert.Contains(t, props, "file")
}
