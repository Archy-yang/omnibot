package skill

import (
	skilldomain "omnibot/internal/domain/skill"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SkillRepository interface {
	// UpsertBuiltin 按 Name upsert 内置技能定义:更新定义字段,**不碰 Enabled**
	// (发版重新 seed 不得覆盖用户启停状态,13-技术方案 §4)。
	UpsertBuiltin(def skilldomain.BuiltinDef) error
	List() ([]*skilldomain.Skill, error)
	GetByName(name string) (*skilldomain.Skill, error)
	SetEnabled(name string, enabled bool) error
}

type skillRepository struct {
	db *gorm.DB
}

func NewSkillRepository(db *gorm.DB) SkillRepository {
	return &skillRepository{db: db}
}

// UpsertBuiltin 定义字段的 upsert。Enabled 不在更新列里——用户启停状态优先于发版。
func (r *skillRepository) UpsertBuiltin(def skilldomain.BuiltinDef) error {
	row := &skilldomain.Skill{
		Name:        def.Name,
		DisplayName: def.DisplayName,
		Description: def.Description,
		Source:      skilldomain.SourceBuiltin,
		Capabilities: skilldomain.JoinCapabilities(def.Capabilities),
		ParamsSchema: skilldomain.MarshalSchema(def.Parameters),
		MainVisible:  def.MainVisible,
		Enabled:     true,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "description", "capabilities", "params_schema", "main_visible", "updated_at",
		}),
	}).Create(row).Error
}

func (r *skillRepository) List() ([]*skilldomain.Skill, error) {
	var rows []*skilldomain.Skill
	err := r.db.Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *skillRepository) GetByName(name string) (*skilldomain.Skill, error) {
	var row skilldomain.Skill
	err := r.db.Where("name = ?", name).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *skillRepository) SetEnabled(name string, enabled bool) error {
	return r.db.Model(&skilldomain.Skill{}).
		Where("name = ?", name).
		Update("enabled", enabled).Error
}
