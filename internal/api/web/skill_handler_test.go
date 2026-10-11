package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"
)

type mockSkillService struct {
	skills        []*skilldomain.Skill
	files         map[int64][]skilldomain.SkillFile
	listErr       error
	created       *skillsvc.CreateInput
	createErr     error
	updatedID     int64
	updateInput   *skillsvc.UpdateInput
	updateErr     error
	deletedID     int64
}

func (m *mockSkillService) List() ([]*skilldomain.Skill, error) {
	return m.skills, m.listErr
}

func (m *mockSkillService) Create(in skillsvc.CreateInput) (*skilldomain.Skill, error) {
	if m.createErr != nil {
		return nil, m.createErr
	}
	m.created = &in
	return &skilldomain.Skill{ID: 7, Name: in.Name, Description: in.Description, Enabled: true, Source: skilldomain.SourceManual}, nil
}

func (m *mockSkillService) Update(id int64, in skillsvc.UpdateInput) error {
	m.updatedID, m.updateInput = id, &in
	return m.updateErr
}

func (m *mockSkillService) GetByID(id int64) (*skilldomain.Skill, error) {
	for _, s := range m.skills {
		if s.ID == id {
			return s, nil
		}
	}
	return nil, skillsvc.ErrNotFound
}

func (m *mockSkillService) Delete(id int64) error {
	m.deletedID = id
	return nil
}

func (m *mockSkillService) ListFiles(skillID int64) ([]skilldomain.SkillFile, error) {
	return m.files[skillID], nil
}

func setupSkillRouter(svc SkillManager) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	h.SetSkillService(svc)
	r.GET("/api/v1/skills", h.HandleListSkills)
	r.GET("/api/v1/skills/:id", h.HandleGetSkill)
	r.POST("/api/v1/skills", h.HandleCreateSkill)
	r.PUT("/api/v1/skills/:id", h.HandleUpdateSkill)
	r.DELETE("/api/v1/skills/:id", h.HandleDeleteSkill)
	return r
}

func skillAPI(t *testing.T, r *gin.Engine, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	w := httptest.NewRecorder()
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	r.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func skillFixture() []*skilldomain.Skill {
	return []*skilldomain.Skill{
		{ID: 1, Name: "charge-analysis", DisplayName: "充电分析", Description: "分析充电记录", Enabled: true, Source: skilldomain.SourceManual},
		{ID: 2, Name: "draft", DisplayName: "草稿技能", Description: "待审", Enabled: false, Source: skilldomain.SourceAgent},
	}
}

// T1 GET 清单:含 file_count;不含 instruction 正文。
func TestHandleListSkills(t *testing.T) {
	mock := &mockSkillService{
		skills: skillFixture(),
		files:  map[int64][]skilldomain.SkillFile{1: {{Path: "analyze.py"}}},
	}
	r := setupSkillRouter(mock)
	code, resp := skillAPI(t, r, http.MethodGet, "/api/v1/skills", "")
	require.Equal(t, http.StatusOK, code)
	data := resp["data"].(map[string]interface{})
	list := data["skills"].([]interface{})
	require.Len(t, list, 2)
	first := list[0].(map[string]interface{})
	assert.Equal(t, "charge-analysis", first["name"])
	assert.Equal(t, float64(1), first["file_count"])
	assert.Equal(t, "manual", first["source"])
	assert.NotContains(t, first, "instruction", "清单不回传正文(编辑时单独取)")
}

// T2 POST 创建:契约回显;非法 name → 400;重名 → 400。
func TestHandleCreateSkill(t *testing.T) {
	mock := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}}
	r := setupSkillRouter(mock)

	body := `{"name":"charge-analysis","display_name":"充电分析","description":"分析充电记录","instruction":"## 步骤","files":[{"path":"analyze.py","content":"print(1)"}]}`
	code, resp := skillAPI(t, r, http.MethodPost, "/api/v1/skills", body)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, resp["success"])
	require.NotNil(t, mock.created)
	assert.Equal(t, "charge-analysis", mock.created.Name)
	require.Len(t, mock.created.Files, 1)

	// 非法 name → 400(校验在 service,handler 测试经由错误映射验证)
	mockInvalid := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}, createErr: skillsvc.ErrNameInvalid}
	rInvalid := setupSkillRouter(mockInvalid)
	code, _ = skillAPI(t, rInvalid, http.MethodPost, "/api/v1/skills", `{"name":"Bad Name","description":"x"}`)
	assert.Equal(t, http.StatusBadRequest, code)

	// 重名 → 400
	mock2 := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}, createErr: skillsvc.ErrNameTaken}
	r2 := setupSkillRouter(mock2)
	code, resp2 := skillAPI(t, r2, http.MethodPost, "/api/v1/skills", body)
	assert.Equal(t, http.StatusBadRequest, code)
	assert.Contains(t, resp2["error"], "已存在")
}

// T3 PUT 更新:name 不可变 → 400;正常更新透传;不存在 → 404。
func TestHandleUpdateSkill(t *testing.T) {
	mock := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}}
	r := setupSkillRouter(mock)

	code, _ := skillAPI(t, r, http.MethodPut, "/api/v1/skills/1", `{"description":"新描述","enabled":false}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, int64(1), mock.updatedID)
	require.NotNil(t, mock.updateInput)
	assert.NotNil(t, mock.updateInput.Enabled)
	assert.False(t, *mock.updateInput.Enabled)
	assert.Nil(t, mock.updateInput.Name, "不带 name 字段时不触发不可变校验")

	// name 变更 → 400(不可变校验在 service,经由错误映射验证)
	mockImmutable := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}, updateErr: skillsvc.ErrNameImmutable}
	rImmutable := setupSkillRouter(mockImmutable)
	code, _ = skillAPI(t, rImmutable, http.MethodPut, "/api/v1/skills/1", `{"name":"other"}`)
	assert.Equal(t, http.StatusBadRequest, code)

	// 不存在 → 404
	mock2 := &mockSkillService{files: map[int64][]skilldomain.SkillFile{}, updateErr: skillsvc.ErrNotFound}
	r2 := setupSkillRouter(mock2)
	code, _ = skillAPI(t, r2, http.MethodPut, "/api/v1/skills/99", `{"description":"x"}`)
	assert.Equal(t, http.StatusNotFound, code)
}

// T4 DELETE:透传;非法 id → 400。
func TestHandleDeleteSkill(t *testing.T) {
	mock := &mockSkillService{}
	r := setupSkillRouter(mock)
	code, resp := skillAPI(t, r, http.MethodDelete, "/api/v1/skills/3", "")
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, resp["success"])
	assert.Equal(t, int64(3), mock.deletedID)

	code, _ = skillAPI(t, r, http.MethodDelete, "/api/v1/skills/abc", "")
	assert.Equal(t, http.StatusBadRequest, code)
}

// T4.5 GET 详情:编辑回显,含 instruction 全文与文件列表;不存在 → 404。
func TestHandleGetSkill(t *testing.T) {
	mock := &mockSkillService{
		skills: skillFixture(),
		files:  map[int64][]skilldomain.SkillFile{1: {{Path: "analyze.py", Content: "print(1)"}}},
	}
	r := setupSkillRouter(mock)

	code, resp := skillAPI(t, r, http.MethodGet, "/api/v1/skills/1", "")
	require.Equal(t, http.StatusOK, code)
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "charge-analysis", data["name"])
	assert.Contains(t, data, "instruction", "详情须含正文(编辑回显)")
	assert.Contains(t, data, "files")
	files := data["files"].([]interface{})
	require.Len(t, files, 1)
	assert.Equal(t, "print(1)", files[0].(map[string]interface{})["content"])

	code, _ = skillAPI(t, r, http.MethodGet, "/api/v1/skills/99", "")
	assert.Equal(t, http.StatusNotFound, code)
}

// T5 service 未注入 → 503(与 tools/mcp 管理面口径一致)。
func TestHandleSkill_NoService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{}
	r.GET("/api/v1/skills", h.HandleListSkills)
	code, _ := skillAPI(t, r, http.MethodGet, "/api/v1/skills", "")
	assert.Equal(t, http.StatusServiceUnavailable, code)
}
