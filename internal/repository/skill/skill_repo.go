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
	// UpsertMCPTool MCP 发现的远端工具 upsert:插入默认停用;更新定义字段不碰 Enabled。
	UpsertMCPTool(def skilldomain.MCPToolDef) error
	// DeleteMCPSkillsNotIn 清理不在配置内的 MCP server 的技能行。
	DeleteMCPSkillsNotIn(serverNames []string) (int64, error)
	// DeleteMCPSkillsByServer 删除指定 server 的全部技能行(server 删除级联)。
	DeleteMCPSkillsByServer(serverName string) (int64, error)
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

// UpsertMCPTool 远端工具 upsert:插入 Enabled=false(发现的技能默认停用,须用户逐个开启);
// 冲突时只更新定义字段,保留用户启停状态。
func (r *skillRepository) UpsertMCPTool(def skilldomain.MCPToolDef) error {
	row := &skilldomain.Skill{
		Name:         def.Name,
		DisplayName:  def.DisplayName,
		Description:  def.Description,
		Source:       skilldomain.SourceMCP,
		ParamsSchema: def.ParamsSchema,
		MainVisible:  def.MainVisible,
		MCPServer:    def.MCPServer,
		Enabled:      def.Enabled,
	}
	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"display_name", "description", "params_schema", "main_visible", "mcp_server", "updated_at",
		}),
	}).Create(row).Error
}

// DeleteMCPSkillsNotIn 删除不属于给定 server 集合的 mcp 技能行。
func (r *skillRepository) DeleteMCPSkillsNotIn(serverNames []string) (int64, error) {
	tx := r.db.Where("source = ?", skilldomain.SourceMCP)
	if len(serverNames) == 0 {
		tx = tx.Where("1 = 1") // 配置内无 server:清掉全部 mcp 技能
	} else {
		tx = tx.Where("mcp_server NOT IN ?", serverNames)
	}
	res := tx.Delete(&skilldomain.Skill{})
	return res.RowsAffected, res.Error
}

// DeleteMCPSkillsByServer 删除指定 server 的全部技能行(server 删除级联)。
func (r *skillRepository) DeleteMCPSkillsByServer(serverName string) (int64, error) {
	res := r.db.Where("source = ? AND mcp_server = ?", skilldomain.SourceMCP, serverName).
		Delete(&skilldomain.Skill{})
	return res.RowsAffected, res.Error
}
