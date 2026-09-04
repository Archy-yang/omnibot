package skill

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"

	mcp "github.com/mark3labs/mcp-go/mcp"
)

// ---- mock MCPServerRepository ----

type mockMCPServerRepository struct {
	servers   []*skilldomain.MCPServer
	nextID    int64
	deletedID int64
	deleted   bool
}

func newMockServerRepo() *mockMCPServerRepository {
	return &mockMCPServerRepository{nextID: 1}
}

func (m *mockMCPServerRepository) Create(s *skilldomain.MCPServer) error {
	if existing, _ := m.GetByName(s.Name); existing != nil {
		return assert.AnError // 唯一索引语义
	}
	s.ID = m.nextID
	m.nextID++
	cp := *s
	m.servers = append(m.servers, &cp)
	return nil
}

func (m *mockMCPServerRepository) Update(s *skilldomain.MCPServer) error {
	for i, r := range m.servers {
		if r.ID == s.ID {
			cp := *s
			m.servers[i] = &cp
			return nil
		}
	}
	return assert.AnError
}

func (m *mockMCPServerRepository) Delete(id int64) error {
	for i, r := range m.servers {
		if r.ID == id {
			m.servers = append(m.servers[:i], m.servers[i+1:]...)
			m.deletedID = id
			m.deleted = true
			return nil
		}
	}
	return assert.AnError
}

func (m *mockMCPServerRepository) GetByID(id int64) (*skilldomain.MCPServer, error) {
	for _, r := range m.servers {
		if r.ID == id {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockMCPServerRepository) GetByName(name string) (*skilldomain.MCPServer, error) {
	for _, r := range m.servers {
		if r.Name == name {
			cp := *r
			return &cp, nil
		}
	}
	return nil, nil
}

func (m *mockMCPServerRepository) List() ([]*skilldomain.MCPServer, error) {
	out := make([]*skilldomain.MCPServer, 0, len(m.servers))
	for _, r := range m.servers {
		cp := *r
		out = append(out, &cp)
	}
	return out, nil
}

func (m *mockMCPServerRepository) Count() (int64, error) { return int64(len(m.servers)), nil }

func newManagerService(serverRepo *mockMCPServerRepository, skillRepo *mockSkillRepository, clients ...*mockMCPClient) *SkillService {
	svc := NewSkillService(skillRepo)
	svc.SetMCPServerRepository(serverRepo)
	svc.SetMCPClientFactory(mockFactory(clients...))
	return svc
}

func enabledClient(tool string) *mockMCPClient {
	return &mockMCPClient{tools: []mcp.Tool{textTool(tool, "远端工具 " + tool)}}
}

// ---- AddServer ----

// 测试 18:AddServer 加密落库(密文非明文)+立即同步发现工具,返回视图含掩码与工具数。
func TestAddServer_EncryptsAndSyncs(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo, enabledClient("gh_search"))

	view, err := svc.AddServer("github", "https://mcp.example.com/mcp", "sk-secret-1", true)
	require.NoError(t, err)
	assert.True(t, view.HasAPIKey)
	assert.Equal(t, 1, view.ToolCount)
	assert.NotContains(t, serverRepo.servers[0].APIKey, "sk-secret-1", "落库必须是密文")
	assert.True(t, strings.HasPrefix(serverRepo.servers[0].APIKey, "enc:"), "密文带前缀标识")

	// 技能已落库(默认停用)
	require.Len(t, skillRepo.upsertedMCP, 1)
	assert.Equal(t, "gh_search", skillRepo.upsertedMCP[0].Name)
	assert.False(t, skillRepo.upsertedMCP[0].Enabled)
}

// 测试 19:AddServer 校验:名称/地址必填,地址必须 http(s),重名拒绝。
func TestAddServer_Validation(t *testing.T) {
	serverRepo := newMockServerRepo()
	svc := newManagerService(serverRepo, &mockSkillRepository{}, enabledClient("t"))

	_, err := svc.AddServer("", "https://x.com", "", true)
	require.Error(t, err)

	_, err = svc.AddServer("a", "ftp://x.com", "", true)
	require.Error(t, err)

	_, err = svc.AddServer("a", "https://x.com", "", true)
	require.NoError(t, err)

	_, err = svc.AddServer("a", "https://y.com", "", true) // 重名
	require.Error(t, err)
}

// 测试 20:AddServer enabled=false 时只落库不连接。
func TestAddServer_DisabledNoConnect(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo)
	client := &mockMCPClient{}
	svc.SetMCPClientFactory(mockFactory(client))

	_, err := svc.AddServer("off", "https://x.com", "", false)
	require.NoError(t, err)
	assert.False(t, client.started, "停用的 server 不得发起连接")
	assert.Empty(t, skillRepo.upsertedMCP)
}

// ---- UpdateServer ----

// 测试 21:UpdateServer 改地址/开关后立即重新同步;密钥留空=保留原 key。
func TestUpdateServer_KeepsKeyAndResyncs(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo, enabledClient("gh_search"))
	_, err := svc.AddServer("github", "https://old.com", "sk-1", true)
	require.NoError(t, err)
	cipherKey := serverRepo.servers[0].APIKey

	// 空 key 更新 → 保留
	view, err := svc.UpdateServer(serverRepo.servers[0].ID, "github", "https://new.com", "", true)
	require.NoError(t, err)
	assert.Equal(t, cipherKey, serverRepo.servers[0].APIKey, "空 key 不得清掉原密钥")
	assert.Equal(t, "https://new.com", view.BaseURL)
}

// 测试 22:UpdateServer 改为 disabled → 移除其执行体(技能隐藏)。
func TestUpdateServer_DisableHidesSkills(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{rows: []*skilldomain.Skill{mcpRow("gh_search", "github", true)}}
	svc := newManagerService(serverRepo, skillRepo, enabledClient("gh_search"))
	view, err := svc.AddServer("github", "https://old.com", "sk-1", true)
	require.NoError(t, err)

	svc.mu.RLock()
	_, had := svc.mcpExecutors["gh_search"]
	svc.mu.RUnlock()
	require.True(t, had)

	_, err = svc.UpdateServer(view.ID, "github", "https://old.com", "", false)
	require.NoError(t, err)
	svc.mu.RLock()
	_, had = svc.mcpExecutors["gh_search"]
	svc.mu.RUnlock()
	assert.False(t, had, "停用后执行体应移除,技能隐藏")
}

// ---- DeleteServer ----

// 测试 23:DeleteServer 级联删技能行+移除执行体。
func TestDeleteServer_CascadesSkills(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{rows: []*skilldomain.Skill{mcpRow("gh_search", "github", true)}}
	svc := newManagerService(serverRepo, skillRepo, enabledClient("gh_search"))
	view, _ := svc.AddServer("github", "https://x.com", "sk-1", true)

	err := svc.DeleteServer(view.ID)
	require.NoError(t, err)
	assert.True(t, serverRepo.deleted)
	assert.Equal(t, []string{"github"}, skillRepo.deletedServers, "级联删除该 server 的技能行")
	svc.mu.RLock()
	_, had := svc.mcpExecutors["gh_search"]
	svc.mu.RUnlock()
	assert.False(t, had)
}

// ---- SyncServer(手动同步) ----

// 测试 24:手动同步发现新工具落库;失败的 server 返回可读错误不 panic。
func TestSyncServer_Manual(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo, &mockMCPClient{startErr: assert.AnError})
	view, _ := svc.AddServer("broken", "https://x.com", "", true) // 同步失败但落库成功

	res, err := svc.SyncServer(view.ID)
	require.NoError(t, err, "同步失败以结果字段表达,不作为调用错误")
	assert.NotEmpty(t, res.Err)
	assert.Equal(t, 0, res.ToolCount)

	// 换成好的 client 再同步
	svc.SetMCPClientFactory(mockFactory(enabledClient("gh_search")))
	res, err = svc.SyncServer(view.ID)
	require.NoError(t, err)
	assert.Empty(t, res.Err)
	assert.Equal(t, 1, res.ToolCount)
}

// ---- ListServers ----

// 测试 25:ListServers 回显掩码视图(无明文),工具数来自该 server 的技能行统计。
func TestListServers_MaskedView(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{rows: []*skilldomain.Skill{
		mcpRow("t1", "github", false),
		mcpRow("t2", "github", false),
		mcpRow("t3", "github", false),
	}}
	svc := newManagerService(serverRepo, skillRepo, &mockMCPClient{}) // 空工具列表,不干扰预置计数
	_, err := svc.AddServer("github", "https://x.com", "sk-abc", true)
	require.NoError(t, err)

	views, err := svc.ListServers()
	require.NoError(t, err)
	require.Len(t, views, 1)
	assert.True(t, views[0].HasAPIKey)
	assert.Equal(t, 3, views[0].ToolCount)
	assert.NotContains(t, views[0].Name, "sk-abc")
}

// ---- 启动同步 ----

// 测试 26:SyncAllServers 从 DB 读配置(解密 key)完成启动同步。
func TestSyncAllServers_FromDB(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo, enabledClient("gh_search"))
	_, err := svc.AddServer("github", "https://x.com", "sk-1", true)
	require.NoError(t, err)

	// 新 service 实例模拟重启:执行体为空,SyncAllServers 重建
	svc2 := NewSkillService(skillRepo)
	svc2.SetMCPServerRepository(serverRepo)
	client := enabledClient("gh_search")
	svc2.SetMCPClientFactory(mockFactory(client))

	require.NoError(t, svc2.SyncAllServers(context.Background()))
	// AddServer 同步一次 + SyncAllServers 再同步一次 → 两次 upsert(每次同步都会 upsert)
	require.Len(t, skillRepo.upsertedMCP, 2)
	svc2.mu.RLock()
	_, had := svc2.mcpExecutors["gh_search"]
	svc2.mu.RUnlock()
	assert.True(t, had)
}

// 测试 27:SeedServersFromConfig 仅当库为空时导入 yaml 配置,返回导入数。
func TestSeedServersFromConfig(t *testing.T) {
	serverRepo := newMockServerRepo()
	skillRepo := &mockSkillRepository{}
	svc := newManagerService(serverRepo, skillRepo)

	n, err := svc.SeedServersFromConfig([]MCPServerSpec{
		{Name: "github", BaseURL: "https://x.com", APIKey: "sk-yaml", Enabled: true},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.NotContains(t, serverRepo.servers[0].APIKey, "sk-yaml", "seed 也要加密落库")

	// 二次调用(库非空)不导入
	n, err = svc.SeedServersFromConfig([]MCPServerSpec{
		{Name: "other", BaseURL: "https://y.com", Enabled: true},
	})
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	assert.Len(t, serverRepo.servers, 1)
}
