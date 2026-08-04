package agent

import (
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	domain "omnibot/internal/domain/agent"
)

func setupArtifactTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger:         logger.Default.LogMode(logger.Silent),
		TranslateError: true,
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&domain.Artifact{}))
	return db
}

func TestArtifactRepository_CreateAndGetByTaskID(t *testing.T) {
	db := setupArtifactTestDB(t)
	repo := NewArtifactRepository(db)
	a := domain.NewMarkdownArtifact(42, "research_report", "# 报告\n内容...")
	require.NoError(t, repo.Create(a))
	assert.NotZero(t, a.ID)

	got, err := repo.GetByTaskID(42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), got.TaskID)
	assert.Equal(t, "research_report", got.Name)
	assert.Equal(t, domain.ArtifactContentTypeMarkdown, got.ContentType)
	assert.Equal(t, "# 报告\n内容...", got.Text())
}

func TestArtifactRepository_ListByTaskID(t *testing.T) {
	db := setupArtifactTestDB(t)
	repo := NewArtifactRepository(db)
	require.NoError(t, repo.Create(domain.NewMarkdownArtifact(1, "a1", "x")))
	require.NoError(t, repo.Create(domain.NewMarkdownArtifact(1, "a2", "y")))
	require.NoError(t, repo.Create(domain.NewMarkdownArtifact(2, "other", "z")))

	list, err := repo.ListByTaskID(1)
	require.NoError(t, err)
	assert.Len(t, list, 2)
	assert.Equal(t, "a1", list[0].Name)
	assert.Equal(t, "a2", list[1].Name)
}

func TestArtifactRepository_GetByTaskID_NotFound(t *testing.T) {
	db := setupArtifactTestDB(t)
	repo := NewArtifactRepository(db)
	_, err := repo.GetByTaskID(999)
	assert.Error(t, err) // gorm.ErrRecordNotFound
}

func TestArtifact_Text_Nil(t *testing.T) {
	var a *domain.Artifact
	assert.Equal(t, "", a.Text())
}
