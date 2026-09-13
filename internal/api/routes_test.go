package api

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 源码守护测试:路由注册与依赖装配拆分后(routes.go/wire.go),
// 断言目标改为「装配在 wire.go、注册在 routes.go」的组合文本。

func readRoutesAndWire(t *testing.T) string {
	t.Helper()
	routes, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	wire, err := os.ReadFile("wire.go")
	require.NoError(t, err)
	return string(routes) + string(wire)
}

func TestSetupRouter_RegistersMemoryRoutes(t *testing.T) {
	content := readRoutesAndWire(t)

	assert.Contains(t, content, "web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)")
	assert.Contains(t, content, "r.Group(\"/api/v1/memories\")")
	assert.Contains(t, content, "HandleGetMemories")
	assert.Contains(t, content, "HandleCreateMemory")
	assert.Contains(t, content, "HandleClearMemories")
}

// v2.1: 认证路由装配
func TestSetupRouter_RegistersAuthRoutes(t *testing.T) {
	content := readRoutesAndWire(t)

	assert.Contains(t, content, "web.NewAuthHandler(authSvc)")
	assert.Contains(t, content, "r.Group(\"/api/v1/auth\")")
	assert.Contains(t, content, "d.authHandler.HandleRegister")
	assert.Contains(t, content, "d.authHandler.HandleLogin")
}

// 拆分守护:routes.go 只做注册(不出现服务构造),wire.go 只做装配(不出现路由组)。
func TestRoutesWire_SeparationOfConcerns(t *testing.T) {
	routesRaw, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	wireRaw, err := os.ReadFile("wire.go")
	require.NoError(t, err)
	routes, wire := string(routesRaw), string(wireRaw)

	assert.NotContains(t, routes, "gorm.Open", "routes.go 不应再做 DB/服务构造")
	assert.NotContains(t, routes, "NewMemoryService(")
	assert.NotContains(t, wire, "r.Group(", "wire.go 不应注册路由")

	// 组合完整性:装配产物都被 routes.go 以 d.* 引用
	for _, dep := range []string{"d.webHandler", "d.agentTaskHandler", "d.subscriptionHandler", "d.realtimeHub"} {
		assert.True(t, strings.Contains(routes, dep), "routes.go 应引用 %s", dep)
	}
	assert.Contains(t, wire, "func buildAppDeps(cfg *config.Config) *appDeps")
}
