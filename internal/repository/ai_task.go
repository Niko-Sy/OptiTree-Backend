package repository

import (
	"errors"

	"optitree-backend/internal/model"

	"gorm.io/gorm"
)

type AITaskRepository struct {
	db *gorm.DB
}

func NewAITaskRepository(db *gorm.DB) *AITaskRepository {
	return &AITaskRepository{db: db}
}

func (r *AITaskRepository) Create(task *model.AITask) error {
	return r.db.Create(task).Error
}

func (r *AITaskRepository) FindByID(id string) (*model.AITask, error) {
	var task model.AITask
	err := r.db.Where("id = ?", id).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &task, err
}

func (r *AITaskRepository) UpdateStatus(id, status string, progress int, stage, stageLabel string) error {
	return r.db.Model(&model.AITask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":      status,
		"progress":    progress,
		"stage":       stage,
		"stage_label": stageLabel,
	}).Error
}

// DocumentRepository 文档仓库
type DocumentRepository struct {
	db *gorm.DB
}

func NewDocumentRepository(db *gorm.DB) *DocumentRepository {
	return &DocumentRepository{db: db}
}

func (r *DocumentRepository) Create(doc *model.Document) error {
	return r.db.Create(doc).Error
}

func (r *DocumentRepository) FindByID(id string) (*model.Document, error) {
	var doc model.Document
	err := r.db.Where("id = ?", id).First(&doc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &doc, err
}

func (r *DocumentRepository) DeleteByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.Document{}).Error
}
