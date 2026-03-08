package repository

import (
	"errors"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type ProjectRepository struct {
	db *gorm.DB
}

func NewProjectRepository(db *gorm.DB) *ProjectRepository {
	return &ProjectRepository{db: db}
}

func (r *ProjectRepository) Create(project *model.Project) error {
	return r.db.Create(project).Error
}

func (r *ProjectRepository) FindByID(id string) (*model.Project, error) {
	var project model.Project
	err := r.db.Where("id = ?", id).First(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &project, err
}

type ProjectListParams struct {
	UserID    string
	Type      string
	Keyword   string
	SortBy    string
	SortOrder string
	Page      int
	PageSize  int
}

func (r *ProjectRepository) List(params ProjectListParams) ([]model.Project, int64, error) {
	var projects []model.Project
	var total int64

	// 查询用户能访问的项目（自己创建 + 成员身份）
	subQuery := r.db.Model(&model.ProjectMember{}).
		Select("project_id").
		Where("user_id = ? AND status = 'active'", params.UserID)

	q := r.db.Model(&model.Project{}).
		Where("created_by = ? OR id IN (?)", params.UserID, subQuery)

	if params.Type != "" {
		q = q.Where("type = ?", params.Type)
	}
	if params.Keyword != "" {
		keyword := "%" + params.Keyword + "%"
		q = q.Where("name ILIKE ?", keyword)
	}

	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	allowedSort := map[string]bool{"created_at": true, "updated_at": true, "name": true}
	sortBy := "updated_at"
	if allowedSort[params.SortBy] {
		sortBy = params.SortBy
	}
	sortOrder := "desc"
	if params.SortOrder == "asc" {
		sortOrder = "asc"
	}

	offset := (params.Page - 1) * params.PageSize
	err := q.Order(sortBy + " " + sortOrder).
		Offset(offset).Limit(params.PageSize).
		Find(&projects).Error

	return projects, total, err
}

func (r *ProjectRepository) Update(project *model.Project) error {
	return r.db.Save(project).Error
}

func (r *ProjectRepository) UpdateFields(id string, fields map[string]interface{}) error {
	return r.db.Model(&model.Project{}).Where("id = ?", id).Updates(fields).Error
}

// UpdateRevision 乐观锁 CAS 更新 revision，返回影响行数
func (r *ProjectRepository) UpdateRevision(id string, oldRev, newRev int) (int64, error) {
	result := r.db.Model(&model.Project{}).
		Where("id = ? AND graph_revision = ?", id, oldRev).
		Update("graph_revision", newRev)
	return result.RowsAffected, result.Error
}

// UpdateCounts 更新项目的统计计数
func (r *ProjectRepository) UpdateCounts(id string, nodeCount, edgeCount, entityCount, relationCount int) error {
	return r.db.Model(&model.Project{}).Where("id = ?", id).Updates(map[string]interface{}{
		"node_count":     nodeCount,
		"edge_count":     edgeCount,
		"entity_count":   entityCount,
		"relation_count": relationCount,
	}).Error
}

func (r *ProjectRepository) UpdateLatestVersion(id string, versionID *string) error {
	return r.db.Model(&model.Project{}).Where("id = ?", id).
		Update("latest_version_id", versionID).Error
}

func (r *ProjectRepository) Delete(tx *gorm.DB, id string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("id = ?", id).Delete(&model.Project{}).Error
}

// CountByUser 统计用户的项目数量，按类型分组
func (r *ProjectRepository) CountByUser(userID string) (ftCount, kgCount int64, err error) {
	subQuery := r.db.Model(&model.ProjectMember{}).
		Select("project_id").
		Where("user_id = ? AND status = 'active'", userID)

	type TypeCount struct {
		Type  string
		Count int64
	}
	var results []TypeCount
	err = r.db.Model(&model.Project{}).
		Select("type, count(*) as count").
		Where("created_by = ? OR id IN (?)", userID, subQuery).
		Group("type").
		Scan(&results).Error
	if err != nil {
		return
	}
	for _, r := range results {
		if r.Type == "ft" {
			ftCount = r.Count
		} else if r.Type == "kg" {
			kgCount = r.Count
		}
	}
	return
}

// SumNodeCounts 统计用户项目的节点总数
func (r *ProjectRepository) SumNodeCounts(userID string) (nodeSum, entitySum int64, err error) {
	subQuery := r.db.Model(&model.ProjectMember{}).
		Select("project_id").
		Where("user_id = ? AND status = 'active'", userID)

	type Sums struct {
		NodeSum   int64
		EntitySum int64
	}
	var result Sums
	err = r.db.Model(&model.Project{}).
		Select("COALESCE(SUM(node_count), 0) as node_sum, COALESCE(SUM(entity_count), 0) as entity_sum").
		Where("created_by = ? OR id IN (?)", userID, subQuery).
		Scan(&result).Error
	nodeSum = result.NodeSum
	entitySum = result.EntitySum
	return
}
