package agent

import (
	"gorm.io/gorm"

	domainagent "omnibot/internal/domain/agent"
)

// ArtifactRepository 子 Agent 产物仓储(10-规划 §2.3)。
type ArtifactRepository interface {
	// Create 写入一个 artifact。
	Create(a *domainagent.Artifact) error
	// GetByTaskID 读取某任务的主产物(按 created_at 升序取第一个;一个任务通常一个主产物)。
	GetByTaskID(taskID int64) (*domainagent.Artifact, error)
	// ListByTaskID 读取某任务的全部产物(按创建时间升序)。
	ListByTaskID(taskID int64) ([]*domainagent.Artifact, error)
}

type artifactRepository struct {
	db *gorm.DB
}

// NewArtifactRepository 创建产物仓储
func NewArtifactRepository(db *gorm.DB) ArtifactRepository {
	return &artifactRepository{db: db}
}

func (r *artifactRepository) Create(a *domainagent.Artifact) error {
	return r.db.Create(a).Error
}

func (r *artifactRepository) GetByTaskID(taskID int64) (*domainagent.Artifact, error) {
	var a domainagent.Artifact
	err := r.db.Where("task_id = ?", taskID).Order("created_at ASC").First(&a).Error
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (r *artifactRepository) ListByTaskID(taskID int64) ([]*domainagent.Artifact, error) {
	var arts []*domainagent.Artifact
	err := r.db.Where("task_id = ?", taskID).Order("created_at ASC").Find(&arts).Error
	if err != nil {
		return nil, err
	}
	return arts, nil
}
