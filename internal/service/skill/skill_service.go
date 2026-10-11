// Package skill 实现技能调度中枢(18-Skill技能系统技术方案 §4)。
// DB 为单一事实源;内存缓存仅存"技能目录"(enabled 技能的 name/description),
// CRUD 后即失效重建——目录注入与 load_skill 即时生效,无需重启。
package skill

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	skilldomain "omnibot/internal/domain/skill"
)

// 语义化错误(handler 映射 400/404)。
var (
	ErrNotFound      = errors.New("skill: not found")
	ErrNameInvalid   = errors.New("skill: name 需为 2~64 位小写字母/数字/连字符")
	ErrNameTaken     = errors.New("skill: name 已存在")
	ErrNameImmutable = errors.New("skill: name 创建后不可变")
	ErrTooLarge      = errors.New("skill: 内容超出大小上限")
	ErrTooManyFiles  = errors.New("skill: 文件数量超出上限")
	ErrBadFilePath   = errors.New("skill: 非法文件路径")
)

// nameRegex 目录 key 格式(18 号 §3):小写字母/数字开头结尾,中间可含连字符,2~64 位。
var nameRegex = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`)

// SkillRepository 持久化窄接口(service 层声明,repository 层实现)。
// Update 的 files 语义:nil = 不动文件;非 nil = 全量替换。
type SkillRepository interface {
	Create(s *skilldomain.Skill, files []skilldomain.SkillFile) error
	Update(s *skilldomain.Skill, files []skilldomain.SkillFile) error
	Delete(id int64) error
	List() ([]*skilldomain.Skill, error)
	GetByID(id int64) (*skilldomain.Skill, error)
	GetByName(name string) (*skilldomain.Skill, error)
	ListFiles(skillID int64) ([]skilldomain.SkillFile, error)
}

// CatalogEntry 技能目录条目(常驻注入的最小集)。
type CatalogEntry struct {
	Name        string
	DisplayName string
	Description string
}

// FileMeta 文件清单项(不含内容,渐进加载)。
type FileMeta struct {
	Path string
}

// FileInput 写入用文件。
type FileInput struct {
	Path    string
	Content string
}

// CreateInput 创建参数。
type CreateInput struct {
	Name        string
	DisplayName string
	Description string
	Instruction string
	Files       []FileInput
}

// UpdateInput 更新参数(指针/字段存在性区分"改"与"不改";name 不可变)。
type UpdateInput struct {
	Name        *string // 非 nil 且 ≠ 原 name → ErrNameImmutable
	DisplayName *string
	Description *string
	Instruction *string
	Enabled     *bool
	Files       []FileInput // nil = 不动;非 nil = 全量替换
}

// SkillService 技能调度中枢。
type SkillService struct {
	repo     SkillRepository
	mu       sync.RWMutex
	catalog  []CatalogEntry
	onChange func() // CRUD 成功后回调(wire 装配:重建主 Agent system prompt)
}

func NewSkillService(repo SkillRepository) *SkillService {
	return &SkillService{repo: repo}
}

// SetOnChange 装配期注入变更回调(幂等安全:回调失败不回滚数据)。
func (s *SkillService) SetOnChange(cb func()) { s.onChange = cb }

func (s *SkillService) notify() {
	if s.onChange != nil {
		s.onChange()
	}
}

// Catalog 当前技能目录(enabled,缓存;RLock)。
func (s *SkillService) Catalog() []CatalogEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.catalog
}

// CatalogPrompt 渲染技能目录段文本;无 enabled 技能返回空串(装配端据此跳过 section)。
func (s *SkillService) CatalogPrompt() string {
	entries := s.Catalog()
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("== 技能目录==\n\n你可以加载以下技能获取特定任务的详细做法(调 load_skill,传 name):\n")
	for _, e := range entries {
		if e.DisplayName != "" {
			fmt.Fprintf(&b, "- %s(%s):%s\n", e.DisplayName, e.Name, e.Description)
		} else {
			fmt.Fprintf(&b, "- %s:%s\n", e.Name, e.Description)
		}
	}
	b.WriteString("目录里没有相关技能时按常规方式处理;用户要求把某个流程沉淀为技能时,调 save_skill 落库(默认停用,告知用户在设置-技能中审阅开启后生效)。")
	return b.String()
}

// LoadInstruction 按 name 加载指令正文 + 文件清单(仅 enabled;disabled/不存在 → ErrNotFound)。
func (s *SkillService) LoadInstruction(name string) (string, []FileMeta, error) {
	sk, err := s.repo.GetByName(name)
	if err != nil || !sk.Enabled {
		return "", nil, ErrNotFound
	}
	files, err := s.repo.ListFiles(sk.ID)
	if err != nil {
		return "", nil, err
	}
	metas := make([]FileMeta, 0, len(files))
	for _, f := range files {
		metas = append(metas, FileMeta{Path: f.Path})
	}
	return sk.Instruction, metas, nil
}

// LoadFile 按 name+path 取文件内容(仅 enabled;路径逃逸拒绝)。
func (s *SkillService) LoadFile(name, path string) (string, error) {
	if err := validateFilePath(path); err != nil {
		return "", err
	}
	sk, err := s.repo.GetByName(name)
	if err != nil || !sk.Enabled {
		return "", ErrNotFound
	}
	files, err := s.repo.ListFiles(sk.ID)
	if err != nil {
		return "", err
	}
	for _, f := range files {
		if f.Path == path {
			return f.Content, nil
		}
	}
	return "", ErrNotFound
}

// Create 创建技能(校验 → 落库 → 刷新目录 → 通知)。
func (s *SkillService) Create(in CreateInput) (*skilldomain.Skill, error) {
	return s.createWith(in, skilldomain.SourceManual, true)
}

// SaveSkillByAgent Agent 自助落库(18 号 §5.3):source=agent、enabled=false——
// 用户审阅后在 Web 端开启才进目录;name 冲突报错(提示换名),不覆盖。
func (s *SkillService) SaveSkillByAgent(in CreateInput) (*skilldomain.Skill, error) {
	return s.createWith(in, skilldomain.SourceAgent, false)
}

// createWith 共享校验与落库管线。
func (s *SkillService) createWith(in CreateInput, source string, enabled bool) (*skilldomain.Skill, error) {
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	files, err := validateAndBuildFiles(in.Files)
	if err != nil {
		return nil, err
	}
	if len(in.Description) == 0 || len(in.Description) > skilldomain.MaxDescriptionBytes {
		return nil, fmt.Errorf("%w: description 必填且 ≤%d 字节", ErrTooLarge, skilldomain.MaxDescriptionBytes)
	}
	if len(in.Instruction) > skilldomain.MaxInstructionBytes {
		return nil, fmt.Errorf("%w: instruction ≤%d 字节", ErrTooLarge, skilldomain.MaxInstructionBytes)
	}
	if _, err := s.repo.GetByName(in.Name); err == nil {
		return nil, ErrNameTaken
	}
	sk := &skilldomain.Skill{
		Name:        in.Name,
		DisplayName: in.DisplayName,
		Description: in.Description,
		Instruction: in.Instruction,
		Source:      source,
		Enabled:     enabled,
	}
	if err := s.repo.Create(sk, files); err != nil {
		return nil, err
	}
	s.refreshCatalog()
	s.notify()
	return sk, nil
}

// Update 更新技能(name 不可变;nil 字段不动;files 非 nil 全量替换)。
func (s *SkillService) Update(id int64, in UpdateInput) error {
	sk, err := s.repo.GetByID(id)
	if err != nil {
		return err
	}
	if in.Name != nil && *in.Name != sk.Name {
		return ErrNameImmutable
	}
	var files []skilldomain.SkillFile
	if in.Files != nil {
		if files, err = validateAndBuildFiles(in.Files); err != nil {
			return err
		}
	}
	if in.Description != nil {
		if len(*in.Description) == 0 || len(*in.Description) > skilldomain.MaxDescriptionBytes {
			return fmt.Errorf("%w: description 必填且 ≤%d 字节", ErrTooLarge, skilldomain.MaxDescriptionBytes)
		}
		sk.Description = *in.Description
	}
	if in.DisplayName != nil {
		sk.DisplayName = *in.DisplayName
	}
	if in.Instruction != nil {
		if len(*in.Instruction) > skilldomain.MaxInstructionBytes {
			return fmt.Errorf("%w: instruction ≤%d 字节", ErrTooLarge, skilldomain.MaxInstructionBytes)
		}
		sk.Instruction = *in.Instruction
	}
	if in.Enabled != nil {
		sk.Enabled = *in.Enabled
	}
	if err := s.repo.Update(sk, files); err != nil {
		return err
	}
	s.refreshCatalog()
	s.notify()
	return nil
}

// Delete 删除技能(级联删文件)。
func (s *SkillService) Delete(id int64) error {
	if err := s.repo.Delete(id); err != nil {
		return err
	}
	s.refreshCatalog()
	s.notify()
	return nil
}

// List 全量清单(管理面;文件数由 handler 按需取 ListFiles)。
func (s *SkillService) List() ([]*skilldomain.Skill, error) {
	return s.repo.List()
}

// GetByID 单个(编辑回显)。
func (s *SkillService) GetByID(id int64) (*skilldomain.Skill, error) {
	return s.repo.GetByID(id)
}

// ListFiles 技能文件清单(编辑回显/文件数徽标)。
func (s *SkillService) ListFiles(skillID int64) ([]skilldomain.SkillFile, error) {
	return s.repo.ListFiles(skillID)
}

// refreshCatalog 从库重建目录缓存。
func (s *SkillService) refreshCatalog() {
	rows, err := s.repo.List()
	if err != nil {
		return // 缓存保守保留旧值;下一次 CRUD 再试(读路径不受影响)
	}
	entries := make([]CatalogEntry, 0, len(rows))
	for _, r := range rows {
		if r.Enabled {
			entries = append(entries, CatalogEntry{
				Name:        r.Name,
				DisplayName: r.DisplayName,
				Description: r.Description,
			})
		}
	}
	s.mu.Lock()
	s.catalog = entries
	s.mu.Unlock()
}

// ---- 校验 ----

func validateName(name string) error {
	if !nameRegex.MatchString(name) {
		return ErrNameInvalid
	}
	return nil
}

func validateAndBuildFiles(in []FileInput) ([]skilldomain.SkillFile, error) {
	if len(in) > skilldomain.MaxFiles {
		return nil, ErrTooManyFiles
	}
	out := make([]skilldomain.SkillFile, 0, len(in))
	seen := map[string]bool{}
	for _, f := range in {
		if err := validateFilePath(f.Path); err != nil {
			return nil, err
		}
		if seen[f.Path] {
			return nil, fmt.Errorf("%w: 重复路径 %q", ErrBadFilePath, f.Path)
		}
		seen[f.Path] = true
		if len(f.Content) > skilldomain.MaxFileBytes {
			return nil, fmt.Errorf("%w: 文件 %q ≤%d 字节", ErrTooLarge, f.Path, skilldomain.MaxFileBytes)
		}
		out = append(out, skilldomain.SkillFile{Path: f.Path, Content: f.Content})
	}
	return out, nil
}

// validateFilePath 文件路径:相对路径、不逃逸(skill_files 与沙箱 Files 同规)。
func validateFilePath(path string) error {
	if path == "" || strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return fmt.Errorf("%w: %q 必须为相对路径且不含 ..", ErrBadFilePath, path)
	}
	return nil
}
