package skill

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
)

// ---- mock repo ----

type mockSkillRepository struct {
	seq    int64
	skills map[int64]*skilldomain.Skill
	files  map[int64][]skilldomain.SkillFile // skillID -> files
}

func newMockSkillRepository() *mockSkillRepository {
	return &mockSkillRepository{
		skills: map[int64]*skilldomain.Skill{},
		files:  map[int64][]skilldomain.SkillFile{},
	}
}

func (m *mockSkillRepository) Create(s *skilldomain.Skill, files []skilldomain.SkillFile) error {
	m.seq++
	s.ID = m.seq
	m.skills[s.ID] = s
	m.files[s.ID] = files
	return nil
}

func (m *mockSkillRepository) Update(s *skilldomain.Skill, files []skilldomain.SkillFile) error {
	if _, ok := m.skills[s.ID]; !ok {
		return ErrNotFound
	}
	m.skills[s.ID] = s
	if files != nil {
		m.files[s.ID] = files
	}
	return nil
}

func (m *mockSkillRepository) Delete(id int64) error {
	if _, ok := m.skills[id]; !ok {
		return ErrNotFound
	}
	delete(m.skills, id)
	delete(m.files, id)
	return nil
}

func (m *mockSkillRepository) List() ([]*skilldomain.Skill, error) {
	out := make([]*skilldomain.Skill, 0, len(m.skills))
	for _, s := range m.skills {
		out = append(out, s)
	}
	return out, nil
}

func (m *mockSkillRepository) GetByID(id int64) (*skilldomain.Skill, error) {
	s, ok := m.skills[id]
	if !ok {
		return nil, ErrNotFound
	}
	return s, nil
}

func (m *mockSkillRepository) GetByName(name string) (*skilldomain.Skill, error) {
	for _, s := range m.skills {
		if s.Name == name {
			return s, nil
		}
	}
	return nil, ErrNotFound
}

func (m *mockSkillRepository) ListFiles(skillID int64) ([]skilldomain.SkillFile, error) {
	return m.files[skillID], nil
}

// ---- helpers ----

func okInput() CreateInput {
	return CreateInput{
		Name:        "charge-analysis",
		DisplayName: "充电分析",
		Description: "分析飞书充电记录表的电耗与成本,按固定口径汇报",
		Instruction: "## 步骤\n1. 用 feishu 读《充电记录》表\n2. 汇报口径:电量/花费/单公里成本",
	}
}

// T1 CRUD 后目录缓存立即反映;disabled 不进目录。
func TestSkillService_CRUD_Cache(t *testing.T) {
	svc := NewSkillService(newMockSkillRepository())

	created, err := svc.Create(okInput())
	require.NoError(t, err)

	catalog := svc.Catalog()
	require.Len(t, catalog, 1)
	assert.Equal(t, "charge-analysis", catalog[0].Name)
	assert.Equal(t, "充电分析", catalog[0].DisplayName)
	assert.Contains(t, catalog[0].Description, "充电记录")

	// 停用 → 目录即时移除
	require.NoError(t, svc.Update(created.ID, UpdateInput{Enabled: boolPtr(false)}))
	assert.Empty(t, svc.Catalog())

	// 重新启用 → 回来
	require.NoError(t, svc.Update(created.ID, UpdateInput{Enabled: boolPtr(true)}))
	assert.Len(t, svc.Catalog(), 1)

	// 删除 → 目录移除
	require.NoError(t, svc.Delete(created.ID))
	assert.Empty(t, svc.Catalog())
}

// T2 校验:非法 name / 重名 / 超限 / name 不可变。
func TestSkillService_Validation(t *testing.T) {
	repo := newMockSkillRepository()
	svc := NewSkillService(repo)
	_, err := svc.Create(okInput())
	require.NoError(t, err)

	// 非法 name 格式
	bad := []string{"", "A", "中文", "a", "-abc", "ab_c", strings.Repeat("a", 65)}
	for _, name := range bad {
		in := okInput()
		in.Name = name
		_, err := svc.Create(in)
		assert.ErrorIs(t, err, ErrNameInvalid, "name=%q 应被拒绝", name)
	}

	// 重名(不覆盖)
	in := okInput()
	in.Description = "另一个"
	_, err = svc.Create(in)
	assert.ErrorIs(t, err, ErrNameTaken)

	// instruction 超 32KB
	big := okInput()
	big.Instruction = strings.Repeat("x", 32*1024+1)
	_, err = svc.Create(big)
	assert.ErrorIs(t, err, ErrTooLarge)

	// 文件数超 10
	many := okInput()
	for i := 0; i < 11; i++ {
		many.Files = append(many.Files, FileInput{Path: "f.py", Content: "x"})
	}
	_, err = svc.Create(many)
	assert.ErrorIs(t, err, ErrTooManyFiles)

	// 单文件超 64KB
	huge := okInput()
	huge.Files = []FileInput{{Path: "f.py", Content: strings.Repeat("x", 64*1024+1)}}
	_, err = svc.Create(huge)
	assert.ErrorIs(t, err, ErrTooLarge)

	// name 不可变
	created, err := repo.GetByName("charge-analysis")
	require.NoError(t, err)
	newName := "other-name"
	err = svc.Update(created.ID, UpdateInput{Name: &newName})
	assert.ErrorIs(t, err, ErrNameImmutable)
}

// T3 目录渲染:name+description 进目录文本;空目录返回空串(装配端据此跳过 section)。
func TestSkillService_CatalogPrompt(t *testing.T) {
	svc := NewSkillService(newMockSkillRepository())
	assert.Empty(t, svc.CatalogPrompt(), "无技能时目录文本应为空")

	_, err := svc.Create(okInput())
	require.NoError(t, err)
	prompt := svc.CatalogPrompt()
	assert.Contains(t, prompt, "load_skill")
	assert.Contains(t, prompt, "charge-analysis")
	assert.Contains(t, prompt, "充电分析")
	assert.Contains(t, prompt, "充电记录")
}

// T4 load:加载正文+文件清单;带 file 返回内容;disabled/不存在 → ErrNotFound。
func TestSkillService_Load(t *testing.T) {
	svc := NewSkillService(newMockSkillRepository())
	in := okInput()
	in.Files = []FileInput{{Path: "analyze.py", Content: "print(1)"}}
	created, err := svc.Create(in)
	require.NoError(t, err)

	instruction, files, err := svc.LoadInstruction("charge-analysis")
	require.NoError(t, err)
	assert.Contains(t, instruction, "汇报口径")
	require.Len(t, files, 1)
	assert.Equal(t, "analyze.py", files[0].Path)

	content, err := svc.LoadFile("charge-analysis", "analyze.py")
	require.NoError(t, err)
	assert.Equal(t, "print(1)", content)

	// 未启用 → 视为不存在
	require.NoError(t, svc.Update(created.ID, UpdateInput{Enabled: boolPtr(false)}))
	_, _, err = svc.LoadInstruction("charge-analysis")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.LoadFile("charge-analysis", "analyze.py")
	assert.ErrorIs(t, err, ErrNotFound)

	// 不存在的技能/文件
	_, _, err = svc.LoadInstruction("no-such")
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = svc.LoadFile("charge-analysis", "../evil.py")
	assert.Error(t, err, "路径逃逸应被拒绝")
}

// T5 OnChange 回调:每次增改删触发一次(wire 装配点用它重建主 Agent system prompt)。
func TestSkillService_OnChange(t *testing.T) {
	svc := NewSkillService(newMockSkillRepository())
	calls := 0
	svc.SetOnChange(func() { calls++ })

	created, err := svc.Create(okInput())
	require.NoError(t, err)
	require.NoError(t, svc.Update(created.ID, UpdateInput{Enabled: boolPtr(false)}))
	require.NoError(t, svc.Delete(created.ID))
	// 失败的创建不触发
	bad := okInput()
	bad.Name = "Bad_Name"
	_, err = svc.Create(bad)
	require.Error(t, err)
	assert.Equal(t, 3, calls)
}

// T6 SaveSkillByAgent:source=agent、enabled=false;重名不覆盖;校验同 Create。
func TestSaveSkillByAgent(t *testing.T) {
	repo := newMockSkillRepository()
	svc := NewSkillService(repo)

	created, err := svc.SaveSkillByAgent(okInput())
	require.NoError(t, err)
	assert.Equal(t, skilldomain.SourceAgent, created.Source)
	assert.False(t, created.Enabled, "Agent 生成默认停用待审")
	assert.Len(t, svc.Catalog(), 0, "停用技能不进目录")

	// 重名 → 不覆盖
	_, err = svc.SaveSkillByAgent(okInput())
	assert.ErrorIs(t, err, ErrNameTaken)
	row, _ := repo.GetByName("charge-analysis")
	assert.Equal(t, skilldomain.SourceAgent, row.Source, "原记录未被覆盖为 manual")

	// 校验透传:非法 name
	bad := okInput()
	bad.Name = "Bad"
	_, err = svc.SaveSkillByAgent(bad)
	assert.ErrorIs(t, err, ErrNameInvalid)
}

func boolPtr(b bool) *bool { return &b }
