package repository

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type ProjectRepository struct {
	db                       *gorm.DB
	rdb                      *redis.Client
	projectDetailCacheEnable bool
	projectDetailTTL         time.Duration
}

func NewProjectRepository(db *gorm.DB, rdb *redis.Client, projectDetailCacheEnable bool, projectDetailTTL time.Duration) *ProjectRepository {
	if projectDetailTTL <= 0 {
		projectDetailTTL = 30 * time.Minute
	}
	return &ProjectRepository{
		db:                       db,
		rdb:                      rdb,
		projectDetailCacheEnable: projectDetailCacheEnable,
		projectDetailTTL:         projectDetailTTL,
	}
}

func (r *ProjectRepository) Create(project *model.Project) error {
	err := r.db.Create(project).Error
	if err == nil {
		r.cacheProjectDetail(context.Background(), project)
	}
	return err
}

func (r *ProjectRepository) FindByID(id string) (*model.Project, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, nil
	}

	cacheKey := r.projectDetailKey(id)
	if r.canUseProjectDetailCache() {
		if cached, err := r.rdb.Get(context.Background(), cacheKey).Bytes(); err == nil {
			var project model.Project
			if err := json.Unmarshal(cached, &project); err == nil {
				return &project, nil
			}
		}
	}

	var project model.Project
	err := r.db.Where("id = ?", id).Take(&project).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	r.cacheProjectDetail(context.Background(), &project)
	return &project, nil
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

	q := r.db.Model(&model.Project{}).
		Where(
			"created_by = ? OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = projects.id AND pm.user_id = ? AND pm.status = 'active')",
			params.UserID,
			params.UserID,
		)

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
	err := r.db.Save(project).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), project.ID)
	}
	return err
}

func (r *ProjectRepository) UpdateFields(id string, fields map[string]interface{}) error {
	err := r.db.Model(&model.Project{}).Where("id = ?", id).Updates(fields).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return err
}

// UpdateRevision 乐观锁 CAS 更新 revision，返回影响行数
func (r *ProjectRepository) UpdateRevision(id string, oldRev, newRev int) (int64, error) {
	result := r.db.Model(&model.Project{}).
		Where("id = ? AND graph_revision = ?", id, oldRev).
		Update("graph_revision", newRev)
	if result.Error == nil && result.RowsAffected > 0 {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return result.RowsAffected, result.Error
}

// UpdateGraphMetaCAS 在单条 SQL 中完成 revision 与统计字段的 CAS 更新。
func (r *ProjectRepository) UpdateGraphMetaCAS(
	tx *gorm.DB,
	id string,
	oldRev, newRev int,
	nodeCount, edgeCount, entityCount, relationCount int,
) (int64, error) {
	if tx == nil {
		tx = r.db
	}
	result := tx.Model(&model.Project{}).
		Where("id = ? AND graph_revision = ?", id, oldRev).
		Updates(map[string]interface{}{
			"graph_revision": newRev,
			"node_count":     nodeCount,
			"edge_count":     edgeCount,
			"entity_count":   entityCount,
			"relation_count": relationCount,
		})
	if result.Error == nil && result.RowsAffected > 0 {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return result.RowsAffected, result.Error
}

// UpdateCounts 更新项目的统计计数
func (r *ProjectRepository) UpdateCounts(id string, nodeCount, edgeCount, entityCount, relationCount int) error {
	err := r.db.Model(&model.Project{}).Where("id = ?", id).Updates(map[string]interface{}{
		"node_count":     nodeCount,
		"edge_count":     edgeCount,
		"entity_count":   entityCount,
		"relation_count": relationCount,
	}).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return err
}

func (r *ProjectRepository) UpdateLatestVersion(id string, versionID *string) error {
	err := r.db.Model(&model.Project{}).Where("id = ?", id).
		Update("latest_version_id", versionID).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return err
}

func (r *ProjectRepository) UpdateGenerationStatus(id string, status *string) error {
	err := r.db.Model(&model.Project{}).Where("id = ?", id).
		Update("generation_status", status).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return err
}

func (r *ProjectRepository) Delete(tx *gorm.DB, id string) error {
	if tx == nil {
		tx = r.db
	}
	err := tx.Where("id = ?", id).Delete(&model.Project{}).Error
	if err == nil {
		r.invalidateProjectDetailCache(context.Background(), id)
	}
	return err
}

func (r *ProjectRepository) canUseProjectDetailCache() bool {
	return r.rdb != nil && r.projectDetailCacheEnable
}

func (r *ProjectRepository) projectDetailKey(id string) string {
	return constant.RedisKeyProjectDetail + strings.TrimSpace(id)
}

func (r *ProjectRepository) cacheProjectDetail(ctx context.Context, project *model.Project) {
	if !r.canUseProjectDetailCache() || project == nil || strings.TrimSpace(project.ID) == "" {
		return
	}
	if data, err := json.Marshal(project); err == nil {
		_ = r.rdb.Set(ctx, r.projectDetailKey(project.ID), data, r.projectDetailTTL).Err()
	}
}

func (r *ProjectRepository) invalidateProjectDetailCache(ctx context.Context, id string) {
	if !r.canUseProjectDetailCache() || strings.TrimSpace(id) == "" {
		return
	}
	_ = r.rdb.Del(ctx, r.projectDetailKey(id)).Err()
}

// CountByUser 统计用户的项目数量，按类型分组
func (r *ProjectRepository) CountByUser(userID string) (ftCount, kgCount int64, err error) {
	type TypeCount struct {
		Type  string
		Count int64
	}
	var results []TypeCount
	err = r.db.Model(&model.Project{}).
		Select("type, count(*) as count").
		Where(
			"created_by = ? OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = projects.id AND pm.user_id = ? AND pm.status = 'active')",
			userID,
			userID,
		).
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
	type Sums struct {
		NodeSum   int64
		EntitySum int64
	}
	var result Sums
	err = r.db.Model(&model.Project{}).
		Select("COALESCE(SUM(node_count), 0) as node_sum, COALESCE(SUM(entity_count), 0) as entity_sum").
		Where(
			"created_by = ? OR EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = projects.id AND pm.user_id = ? AND pm.status = 'active')",
			userID,
			userID,
		).
		Scan(&result).Error
	nodeSum = result.NodeSum
	entitySum = result.EntitySum
	return
}
