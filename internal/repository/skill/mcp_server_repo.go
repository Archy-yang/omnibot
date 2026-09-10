package skill

import (
	skilldomain "omnibot/internal/domain/skill"

	"gorm.io/gorm"
)

// MCPServerRepository MCP server 配置持久化。
type MCPServerRepository interface {
	Create(server *skilldomain.MCPServer) error
	Update(server *skilldomain.MCPServer) error
	Delete(id int64) error
	GetByID(id int64) (*skilldomain.MCPServer, error)
	GetByName(name string) (*skilldomain.MCPServer, error)
	List() ([]*skilldomain.MCPServer, error)
	Count() (int64, error)
}

type mcpServerRepository struct {
	db *gorm.DB
}

func NewMCPServerRepository(db *gorm.DB) MCPServerRepository {
	return &mcpServerRepository{db: db}
}

func (r *mcpServerRepository) Create(server *skilldomain.MCPServer) error {
	return r.db.Create(server).Error
}

func (r *mcpServerRepository) Update(server *skilldomain.MCPServer) error {
	return r.db.Save(server).Error
}

func (r *mcpServerRepository) Delete(id int64) error {
	return r.db.Delete(&skilldomain.MCPServer{}, id).Error
}

func (r *mcpServerRepository) GetByID(id int64) (*skilldomain.MCPServer, error) {
	var row skilldomain.MCPServer
	if err := r.db.First(&row, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *mcpServerRepository) GetByName(name string) (*skilldomain.MCPServer, error) {
	var row skilldomain.MCPServer
	if err := r.db.Where("name = ?", name).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *mcpServerRepository) List() ([]*skilldomain.MCPServer, error) {
	var rows []*skilldomain.MCPServer
	err := r.db.Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *mcpServerRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&skilldomain.MCPServer{}).Count(&count).Error
	return count, err
}
