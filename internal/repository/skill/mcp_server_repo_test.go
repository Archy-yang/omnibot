package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
)

func TestMCPServerRepo_CRUD(t *testing.T) {
	db := setupSkillTestDB(t)
	require.NoError(t, db.AutoMigrate(&skilldomain.MCPServer{}))
	repo := NewMCPServerRepository(db)

	// Create
	srv := &skilldomain.MCPServer{Name: "github", BaseURL: "https://mcp.example.com/mcp", APIKey: "cipher-text", Enabled: true}
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
	require.NoError(t, repo.Create(&skilldomain.MCPServer{Name: "notion", BaseURL: "https://n.example.com", Enabled: false}))
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
