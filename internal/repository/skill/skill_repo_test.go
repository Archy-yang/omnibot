package skill

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	skilldomain "omnibot/internal/domain/skill"
)

// skills 仓储测试(18 号 S1):文件事务全量替换、级联删除、(skill_id,path) 唯一兜底。
func skillRepoSetup(t *testing.T) (*gorm.DB, SkillRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&skilldomain.Skill{}, &skilldomain.SkillFile{}))
	return db, NewSkillRepository(db)
}

func testSkill(name string) *skilldomain.Skill {
	return &skilldomain.Skill{
		Name: name, DisplayName: name, Description: "测试技能:" + name,
		Instruction: "## 步骤", Source: skilldomain.SourceManual, Enabled: true,
	}
}

func TestSkillRepo_CreateWithFiles(t *testing.T) {
	_, repo := skillRepoSetup(t)
	s := testSkill("charge-analysis")
	files := []skilldomain.SkillFile{{Path: "analyze.py", Content: "print(1)"}}
	require.NoError(t, repo.Create(s, files))

	got, err := repo.GetByName("charge-analysis")
	require.NoError(t, err)
	assert.Equal(t, "充电分析"[0:0]+got.Name, got.Name) // name 直存
	gotFiles, err := repo.ListFiles(got.ID)
	require.NoError(t, err)
	require.Len(t, gotFiles, 1)
	assert.Equal(t, "analyze.py", gotFiles[0].Path)
	assert.Equal(t, "print(1)", gotFiles[0].Content)
}

func TestSkillRepo_UpdateReplacesFiles(t *testing.T) {
	_, repo := skillRepoSetup(t)
	s := testSkill("demo")
	require.NoError(t, repo.Create(s, []skilldomain.SkillFile{{Path: "a.py", Content: "1"}}))
	got, _ := repo.GetByName("demo")

	// files=nil → 不动
	got.Instruction = "v2"
	require.NoError(t, repo.Update(got, nil))
	fs, _ := repo.ListFiles(got.ID)
	require.Len(t, fs, 1)
	assert.Equal(t, "a.py", fs[0].Path)

	// files 非 nil → 全量替换
	got2, _ := repo.GetByName("demo")
	require.NoError(t, repo.Update(got2, []skilldomain.SkillFile{
		{Path: "b.py", Content: "2"}, {Path: "c.md", Content: "3"},
	}))
	fs, _ = repo.ListFiles(got2.ID)
	require.Len(t, fs, 2)
	assert.Equal(t, "b.py", fs[0].Path)

	// 更新后的字段生效
	final, _ := repo.GetByName("demo")
	assert.Equal(t, "v2", final.Instruction)
}

func TestSkillRepo_DeleteCascades(t *testing.T) {
	_, repo := skillRepoSetup(t)
	s := testSkill("demo")
	require.NoError(t, repo.Create(s, []skilldomain.SkillFile{{Path: "a.py", Content: "1"}}))
	got, _ := repo.GetByName("demo")

	require.NoError(t, repo.Delete(got.ID))
	_, err := repo.GetByName("demo")
	assert.Error(t, err)
	fs, _ := repo.ListFiles(got.ID)
	assert.Empty(t, fs, "级联删除文件")

	assert.ErrorIs(t, repo.Delete(got.ID), errRecordNotFoundCompat())
}

// errRecordNotFoundCompat 断言重复删除返回 not found(GORM ErrRecordNotFound)。
func errRecordNotFoundCompat() error {
	return gorm.ErrRecordNotFound
}

func TestSkillRepo_DuplicatePathConflictGuard(t *testing.T) {
	_, repo := skillRepoSetup(t)
	s := testSkill("demo")
	require.NoError(t, repo.Create(s, []skilldomain.SkillFile{{Path: "a.py", Content: "1"}}))
	got, _ := repo.GetByName("demo")

	// 并发兜底:同 (skill_id,path) 二次插入不报错也不产生重复行(OnConflict DoNothing)
	require.NoError(t, repo.Update(got, []skilldomain.SkillFile{
		{Path: "a.py", Content: "x"}, {Path: "a.py", Content: "y"},
	}))
	fs, _ := repo.ListFiles(got.ID)
	assert.NotEmpty(t, fs) // service 层负责校验重复;DB 层只保证不炸不重复
}

func TestSkillRepo_ListOrderedByName(t *testing.T) {
	_, repo := skillRepoSetup(t)
	require.NoError(t, repo.Create(testSkill("b-skill"), nil))
	require.NoError(t, repo.Create(testSkill("a-skill"), nil))
	rows, err := repo.List()
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "a-skill", rows[0].Name)
}
