package repository

import (
	"errors"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type VersionRepository struct {
	db *gorm.DB
}

func NewVersionRepository(db *gorm.DB) *VersionRepository {
	return &VersionRepository{db: db}
}

func (r *VersionRepository) Create(v *model.VersionSnapshot) error {
	return r.db.Create(v).Error
}

func (r *VersionRepository) FindByID(id string) (*model.VersionSnapshot, error) {
	var v model.VersionSnapshot
	err := r.db.Where("id = ?", id).First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}

func (r *VersionRepository) ListByProject(projectID string, page, pageSize int) ([]model.VersionSnapshot, int64, error) {
	var versions []model.VersionSnapshot
	var total int64
	q := r.db.Model(&model.VersionSnapshot{}).Where("project_id = ?", projectID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	offset := (page - 1) * pageSize
	err := q.Select("id, project_id, project_type, label, created_at, created_by").
		Order("created_at DESC").
		Offset(offset).Limit(pageSize).
		Find(&versions).Error
	return versions, total, err
}

func (r *VersionRepository) CountByProject(projectID string) (int64, error) {
	var count int64
	err := r.db.Model(&model.VersionSnapshot{}).Where("project_id = ?", projectID).Count(&count).Error
	return count, err
}

// GetOldestByProject 获取最旧的一条版本
func (r *VersionRepository) GetOldestByProject(projectID string) (*model.VersionSnapshot, error) {
	var v model.VersionSnapshot
	err := r.db.Where("project_id = ?", projectID).Order("created_at ASC").First(&v).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &v, err
}

func (r *VersionRepository) Delete(id string) error {
	return r.db.Where("id = ?", id).Delete(&model.VersionSnapshot{}).Error
}

func (r *VersionRepository) DeleteByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.VersionSnapshot{}).Error
}
