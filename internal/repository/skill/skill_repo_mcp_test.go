package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
)

// 测试 8:发现的远端工具插入默认停用;重复同步更新定义字段、保留用户启停。
func TestSkillRepo_UpsertMCPTool_InsertDisabledKeepEnabled(t *testing.T) {
	db := setupSkillTestDB(t)
	repo := NewSkillRepository(db)

	def := skilldomain.MCPToolDef{
		Name:         "gh_search",
		DisplayName:  "gh_search",
		Description:  "搜 GitHub",
		MCPServer:    "github",
		ParamsSchema: `{"type":"object"}`,
		MainVisible:  true,
		Enabled:      false,
	}
	require.NoError(t, repo.UpsertMCPTool(def))

	row, err := repo.GetByName("gh_search")
	require.NoError(t, err)
	require.NotNil(t, row)
	assert.False(t, row.Enabled, "发现的技能默认停用")
	assert.Equal(t, skilldomain.SourceMCP, row.Source)
	assert.Equal(t, "github", row.MCPServer)

	// 用户开启
	require.NoError(t, repo.SetEnabled("gh_search", true))

	// server 重启再同步:描述更新,启停状态保留
	def.Description = "搜 GitHub 仓库"
	require.NoError(t, repo.UpsertMCPTool(def))
	row, err = repo.GetByName("gh_search")
	require.NoError(t, err)
	assert.Equal(t, "搜 GitHub 仓库", row.Description)
	assert.True(t, row.Enabled, "重复同步不得覆盖用户启停状态")
}

// 测试 12:配置内不存在的 server 的 mcp 技能行被清理;builtin 与配置内 server 的行保留。
func TestSkillRepo_DeleteMCPSkillsNotIn(t *testing.T) {
	db := setupSkillTestDB(t)
	repo := NewSkillRepository(db)

	require.NoError(t, repo.UpsertMCPTool(skilldomain.MCPToolDef{
		Name: "keep_tool", DisplayName: "k", Description: "d",
		MCPServer: "alive", ParamsSchema: "{}", Enabled: false,
	}))
	require.NoError(t, repo.UpsertMCPTool(skilldomain.MCPToolDef{
		Name: "drop_tool", DisplayName: "d", Description: "d",
		MCPServer: "removed", ParamsSchema: "{}", Enabled: false,
	}))
	require.NoError(t, repo.UpsertBuiltin(skilldomain.BuiltinDef{
		Name: "calculator", DisplayName: "c", Description: "d",
		Capabilities: []string{"basic"}, Parameters: map[string]interface{}{"type": "object"},
	}))

	// 保留 alive server 的技能;calculator 是 builtin,不在此清理范围
	n, err := repo.DeleteMCPSkillsNotIn([]string{"alive"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)

	row, err := repo.GetByName("drop_tool")
	require.NoError(t, err)
	assert.Nil(t, row)

	row, err = repo.GetByName("keep_tool")
	require.NoError(t, err)
	require.NotNil(t, row)

	row, err = repo.GetByName("calculator")
	require.NoError(t, err)
	require.NotNil(t, row, "builtin 不受 mcp 清理影响")

	// 配置为空:清掉全部 mcp 技能
	n, err = repo.DeleteMCPSkillsNotIn(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	_, err = repo.GetByName("calculator")
	require.NoError(t, err) // builtin 仍在
}
