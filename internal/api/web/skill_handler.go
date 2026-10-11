package web

import (
	"errors"
	"net/http"
	"strconv"

	skilldomain "omnibot/internal/domain/skill"
	skillsvc "omnibot/internal/service/skill"

	"github.com/gin-gonic/gin"
)

// SkillManager 技能管理窄接口(web 层声明,service 层实现)。
type SkillManager interface {
	List() ([]*skilldomain.Skill, error)
	GetByID(id int64) (*skilldomain.Skill, error)
	Create(in skillsvc.CreateInput) (*skilldomain.Skill, error)
	Update(id int64, in skillsvc.UpdateInput) error
	Delete(id int64) error
	ListFiles(skillID int64) ([]skilldomain.SkillFile, error)
}

// SetSkillService 注入技能服务(技能管理 API)。未注入时技能接口返回 503。
func (h *Handler) SetSkillService(svc SkillManager) {
	h.skillService = svc
}

// SkillView 清单视图(不含 instruction 正文——编辑回显时单独取,清单轻量)。
type SkillView struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	Source      string `json:"source"`
	FileCount   int    `json:"file_count"`
}

func (h *Handler) toSkillView(s *skilldomain.Skill, fileCount int) SkillView {
	return SkillView{
		ID: s.ID, Name: s.Name, DisplayName: s.DisplayName,
		Description: s.Description, Enabled: s.Enabled,
		Source: s.Source, FileCount: fileCount,
	}
}

// mapSkillError 语义化错误 → HTTP 状态与可读文案。
func mapSkillError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, skillsvc.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"success": false, "error": "技能不存在"})
	case errors.Is(err, skillsvc.ErrNameImmutable):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "技能 name 创建后不可修改"})
	case errors.Is(err, skillsvc.ErrNameTaken):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "技能 name 已存在,请换一个"})
	case errors.Is(err, skillsvc.ErrNameInvalid),
		errors.Is(err, skillsvc.ErrTooLarge),
		errors.Is(err, skillsvc.ErrTooManyFiles),
		errors.Is(err, skillsvc.ErrBadFilePath):
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "技能操作失败"})
	}
}

// HandleListSkills GET /api/v1/skills — 技能清单(含文件数,不含正文)。
func (h *Handler) HandleListSkills(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	rows, err := h.skillService.List()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "获取技能清单失败"})
		return
	}
	views := make([]SkillView, 0, len(rows))
	for _, s := range rows {
		count := 0
		if files, ferr := h.skillService.ListFiles(s.ID); ferr == nil {
			count = len(files)
		}
		views = append(views, h.toSkillView(s, count))
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": gin.H{"skills": views}})
}

// skillFileRequest 请求体里的附属文件。
type skillFileRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// createSkillRequest POST /api/v1/skills 请求体。
type createSkillRequest struct {
	Name        string             `json:"name" binding:"required"`
	DisplayName string             `json:"display_name"`
	Description string             `json:"description" binding:"required"`
	Instruction string             `json:"instruction"`
	Files       []skillFileRequest `json:"files"`
}

// HandleGetSkill GET /api/v1/skills/:id — 技能详情(编辑回显:含 instruction 全文与文件)。
func (h *Handler) HandleGetSkill(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "非法技能 id"})
		return
	}
	sk, err := h.skillService.GetByID(id)
	if err != nil {
		mapSkillError(c, err)
		return
	}
	files, err := h.skillService.ListFiles(id)
	if err != nil {
		mapSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data": gin.H{
			"id": sk.ID, "name": sk.Name, "display_name": sk.DisplayName,
			"description": sk.Description, "instruction": sk.Instruction,
			"enabled": sk.Enabled, "source": sk.Source,
			"files": files,
		},
	})
}

// HandleCreateSkill POST /api/v1/skills — 创建技能(默认启用)。
func (h *Handler) HandleCreateSkill(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	var req createSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误(name/description 必填)"})
		return
	}
	created, err := h.skillService.Create(skillsvc.CreateInput{
		Name:        req.Name,
		DisplayName: req.DisplayName,
		Description: req.Description,
		Instruction: req.Instruction,
		Files:       toFileInputs(req.Files),
	})
	if err != nil {
		mapSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "data": h.toSkillView(created, len(req.Files))})
}

// updateSkillRequest PUT /api/v1/skills/:id 请求体(指针字段区分"改"与"不改")。
type updateSkillRequest struct {
	Name        *string            `json:"name"`
	DisplayName *string            `json:"display_name"`
	Description *string            `json:"description"`
	Instruction *string            `json:"instruction"`
	Enabled     *bool              `json:"enabled"`
	Files       *[]skillFileRequest `json:"files"` // nil = 不动;非 nil = 全量替换(空数组 = 清空)
}

// HandleUpdateSkill PUT /api/v1/skills/:id — 更新(name 不可变)。
func (h *Handler) HandleUpdateSkill(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "非法技能 id"})
		return
	}
	var req updateSkillRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "请求体格式错误"})
		return
	}
	in := skillsvc.UpdateInput{Name: req.Name, DisplayName: req.DisplayName, Description: req.Description,
		Instruction: req.Instruction, Enabled: req.Enabled}
	if req.Files != nil {
		in.Files = toFileInputs(*req.Files)
	}
	if err := h.skillService.Update(id, in); err != nil {
		mapSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

// HandleDeleteSkill DELETE /api/v1/skills/:id — 删除(级联删文件)。
func (h *Handler) HandleDeleteSkill(c *gin.Context) {
	if h.skillService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "技能服务未启用"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "非法技能 id"})
		return
	}
	if err := h.skillService.Delete(id); err != nil {
		mapSkillError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true})
}

func toFileInputs(in []skillFileRequest) []skillsvc.FileInput {
	out := make([]skillsvc.FileInput, 0, len(in))
	for _, f := range in {
		out = append(out, skillsvc.FileInput{Path: f.Path, Content: f.Content})
	}
	return out
}
