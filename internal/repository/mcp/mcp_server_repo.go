package mcp

import (
	mcpdomain "omnibot/internal/domain/mcp"

	"gorm.io/gorm"
)

// MCPServerRepository MCP server 配置持久化。
type MCPServerRepository interface {
	Create(server *mcpdomain.MCPServer) error
	Update(server *mcpdomain.MCPServer) error
	Delete(id int64) error
	GetByID(id int64) (*mcpdomain.MCPServer, error)
	GetByName(name string) (*mcpdomain.MCPServer, error)
	List() ([]*mcpdomain.MCPServer, error)
	Count() (int64, error)
}

type mcpServerRepository struct {
	db *gorm.DB
}

func NewMCPServerRepository(db *gorm.DB) MCPServerRepository {
	return &mcpServerRepository{db: db}
}

func (r *mcpServerRepository) Create(server *mcpdomain.MCPServer) error {
	return r.db.Create(server).Error
}

func (r *mcpServerRepository) Update(server *mcpdomain.MCPServer) error {
	return r.db.Save(server).Error
}

func (r *mcpServerRepository) Delete(id int64) error {
	return r.db.Delete(&mcpdomain.MCPServer{}, id).Error
}

func (r *mcpServerRepository) GetByID(id int64) (*mcpdomain.MCPServer, error) {
	var row mcpdomain.MCPServer
	if err := r.db.First(&row, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *mcpServerRepository) GetByName(name string) (*mcpdomain.MCPServer, error) {
	var row mcpdomain.MCPServer
	if err := r.db.Where("name = ?", name).First(&row).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

func (r *mcpServerRepository) List() ([]*mcpdomain.MCPServer, error) {
	var rows []*mcpdomain.MCPServer
	err := r.db.Order("id ASC").Find(&rows).Error
	return rows, err
}

func (r *mcpServerRepository) Count() (int64, error) {
	var count int64
	err := r.db.Model(&mcpdomain.MCPServer{}).Count(&count).Error
	return count, err
}
