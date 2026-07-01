package api

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupRouter_RegistersMemoryRoutes(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	content := string(source)

	assert.Contains(t, content, "web.NewHandler(userSvc, msgSvc, llmClient, llmConfigSvc, memorySvc, agentSvc)")
	assert.Contains(t, content, "r.Group(\"/api/v1/memories\")")
	assert.Contains(t, content, "HandleGetMemories")
	assert.Contains(t, content, "HandleCreateMemory")
	assert.Contains(t, content, "HandleClearMemories")
}

// v2.1: 认证路由装配
func TestSetupRouter_RegistersAuthRoutes(t *testing.T) {
	source, err := os.ReadFile("routes.go")
	require.NoError(t, err)
	content := string(source)

	assert.Contains(t, content, "web.NewAuthHandler(authSvc)")
	assert.Contains(t, content, "r.Group(\"/api/v1/auth\")")
	assert.Contains(t, content, "authHandler.HandleRegister")
	assert.Contains(t, content, "authHandler.HandleLogin")
}
