// Package skill 技能仓储(GORM)。
// 18-Skill技能系统技术方案 §3:skills + skill_files 两表;文件随技能事务落库,
// Delete 级联删文件;Update files 语义:nil = 不动,非 nil = 全量替换。
package skill

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	skilldomain "omnibot/internal/domain/skill"
)

// SkillRepository 持久化窄接口(与 service/skill 声明一致,编译期互证)。
type SkillRepository interface {
	Create(s *skilldomain.Skill, files []skilldomain.SkillFile) error
	Update(s *skilldomain.Skill, files []skilldomain.SkillFile) error
	Delete(id int64) error
	List() ([]*skilldomain.Skill, error)
	GetByID(id int64) (*skilldomain.Skill, error)
	GetByName(name string) (*skilldomain.Skill, error)
	ListFiles(skillID int64) ([]skilldomain.SkillFile, error)
}

type skillRepository struct {
	db *gorm.DB
}

func NewSkillRepository(db *gorm.DB) SkillRepository {
	return &skillRepository{db: db}
}

func (r *skillRepository) Create(s *skilldomain.Skill, files []skilldomain.SkillFile) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(s).Error; err != nil {
			return err
		}
		return createFiles(tx, s.ID, files)
	})
}

func (r *skillRepository) Update(s *skilldomain.Skill, files []skilldomain.SkillFile) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		// name 不可变在 service 校验;此处用 Session 防止 Update 零值字段被 GORM 忽略
		if err := tx.Session(&gorm.Session{FullSaveAssociations: false}).Select(
			"display_name", "description", "instruction", "enabled", "updated_at",
		).Updates(s).Error; err != nil {
			return err
		}
		if files == nil {
			return nil
		}
		if err := tx.Where("skill_id = ?", s.ID).Delete(&skilldomain.SkillFile{}).Error; err != nil {
			return err
		}
		return createFiles(tx, s.ID, files)
	})
}

func (r *skillRepository) Delete(id int64) error {
	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("skill_id = ?", id).Delete(&skilldomain.SkillFile{}).Error; err != nil {
			return err
		}
		res := tx.Delete(&skilldomain.Skill{}, id)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return nil
	})
}

func (r *skillRepository) List() ([]*skilldomain.Skill, error) {
	var rows []*skilldomain.Skill
	err := r.db.Order("name ASC").Find(&rows).Error
	return rows, err
}

func (r *skillRepository) GetByID(id int64) (*skilldomain.Skill, error) {
	var s skilldomain.Skill
	if err := r.db.First(&s, id).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *skillRepository) GetByName(name string) (*skilldomain.Skill, error) {
	var s skilldomain.Skill
	if err := r.db.Where("name = ?", name).First(&s).Error; err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *skillRepository) ListFiles(skillID int64) ([]skilldomain.SkillFile, error) {
	var rows []skilldomain.SkillFile
	err := r.db.Where("skill_id = ?", skillID).Order("path ASC").Find(&rows).Error
	return rows, err
}

// createFiles 批量写入文件(同 skill 内 path 唯一由 service 校验,DB 侧防并发兜底)。
func createFiles(tx *gorm.DB, skillID int64, files []skilldomain.SkillFile) error {
	for i := range files {
		files[i].SkillID = skillID
	}
	if len(files) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&files).Error
}
