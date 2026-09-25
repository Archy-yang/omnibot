package mcp

import (
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	mcpdomain "omnibot/internal/domain/mcp"
)

func TestMCPServerRepo_CRUD(t *testing.T) {
	db := setupMCPServerTestDB(t)
	require.NoError(t, db.AutoMigrate(&mcpdomain.MCPServer{}))
	repo := NewMCPServerRepository(db)

	// Create
	srv := &mcpdomain.MCPServer{Name: "github", BaseURL: "https://mcp.example.com/mcp", APIKey: "cipher-text", Enabled: true}
	require.NoError(t, repo.Create(srv))
	require.NotZero(t, srv.ID)

	// GetByID / GetByName
	byID, err := repo.GetByID(srv.ID)
	require.NoError(t, err)
	require.NotNil(t, byID)
	assert.Equal(t, "github", byID.Name)

	byName, err := repo.GetByName("github")
	require.NoError(t, err)
	require.NotNil(t, byName)
	assert.Equal(t, srv.ID, byName.ID)

	missing, err := repo.GetByName("nope")
	require.NoError(t, err)
	assert.Nil(t, missing)

	// Update
	byID.BaseURL = "https://new.example.com/mcp"
	require.NoError(t, repo.Update(byID))
	got, _ := repo.GetByID(srv.ID)
	assert.Equal(t, "https://new.example.com/mcp", got.BaseURL)

	// List / Count
	require.NoError(t, repo.Create(&mcpdomain.MCPServer{Name: "notion", BaseURL: "https://n.example.com", Enabled: false}))
	rows, err := repo.List()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	cnt, err := repo.Count()
	require.NoError(t, err)
	assert.Equal(t, int64(2), cnt)

	// Delete
	require.NoError(t, repo.Delete(srv.ID))
	gone, err := repo.GetByID(srv.ID)
	require.NoError(t, err)
	assert.Nil(t, gone)
}

// setupMCPServerTestDB 独立内存库(与 tool 包测试分离后自持)。
func setupMCPServerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	return db
}
