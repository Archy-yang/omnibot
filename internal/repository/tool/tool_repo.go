package tool

import (
	tooldomain "omnibot/internal/domain/tool"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type ToolRepository interface {
	// UpsertBuiltin 按 Name upsert 内置工具定义:更新定义字段,**不碰 Enabled**
	// (发版重新 seed 不得覆盖用户启停状态,13-技术方案 §4)。
	UpsertBuiltin(def tooldomain.ToolDef) error
	List() ([]*tooldomain.Tool, error)
	GetByName(name string) (*tooldomain.Tool, error)
	SetEnabled(name string, enabled bool) error
}

type toolRepository struct {
	db *gorm.DB
}

func NewToolRepository(db *gorm.DB) ToolRepository {
	return &toolRepository{db: db}
}

// UpsertBuiltin 定义字段的 upsert。Enabled 不在更新列里——用户启停状态优先于发版。
func (r *toolRepository) UpsertBuiltin(def tooldomain.ToolDef) error {
	row := &tooldomain.Tool{
		Name:         def.Name,
		DisplayName:  def.DisplayName,
		Description:  def.Description,
		Capabilities: tooldomain.JoinCapabilities(def.Capabilities),
		ParamsSchema: tooldomain.MarshalSchema(def.Parameters),
		MainVisible:  def.MainVisible,
		Enabled:      true,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "description", "capabilities", "params_schema", "main_visible", "updated_at",
		}),
	}).Create(row).Error
}

func (r *toolRepository) List() ([]*tooldomain.Tool, error) {
	var rows []*tooldomain.Tool
	err := r.db.Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *toolRepository) GetByName(name string) (*tooldomain.Tool, error) {
	var row tooldomain.Tool
	err := r.db.Where("name = ?", name).First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *toolRepository) SetEnabled(name string, enabled bool) error {
	return r.db.Model(&tooldomain.Tool{}).
		Where("name = ?", name).
		Update("enabled", enabled).Error
}
