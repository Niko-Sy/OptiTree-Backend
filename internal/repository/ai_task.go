package repository

import (
	"errors"
	"strings"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"

	"gorm.io/datatypes"
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

func (r *AITaskRepository) FindByIdempotencyKey(key string) (*model.AITask, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, nil
	}

	var task model.AITask
	err := r.db.Where("idempotency_key = ?", key).First(&task).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &task, err
}

func (r *AITaskRepository) FindLatestByProjectAndType(projectID, taskType string) (*model.AITask, error) {
	var task model.AITask
	err := r.db.Where("project_id = ? AND type = ?", projectID, taskType).
		Order("created_at DESC").
		First(&task).Error
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

// UpdateStatusExt updates task status with worker/attempt metadata.
func (r *AITaskRepository) UpdateStatusExt(
	id, status string,
	progress int,
	stage, stageLabel string,
	workerID *string,
	attemptCount int,
	errorMessage *string,
) error {
	updates := map[string]interface{}{
		"status":        status,
		"progress":      progress,
		"stage":         stage,
		"stage_label":   stageLabel,
		"attempt_count": attemptCount,
	}

	if workerID != nil && strings.TrimSpace(*workerID) != "" {
		updates["worker_id"] = strings.TrimSpace(*workerID)
	}
	if errorMessage != nil {
		updates["error_message"] = strings.TrimSpace(*errorMessage)
	}

	if status == constant.AITaskStatusProcessing || status == constant.AITaskStatusRetrying {
		updates["started_at"] = gorm.Expr("COALESCE(started_at, NOW())")
	}
	if status == constant.AITaskStatusCompleted || status == constant.AITaskStatusFailed || status == constant.AITaskStatusDead {
		updates["completed_at"] = gorm.Expr("NOW()")
	}

	return r.db.Model(&model.AITask{}).Where("id = ?", id).Updates(updates).Error
}

// SetCompleted marks a task as completed and stores its result JSON.
func (r *AITaskRepository) SetCompleted(id string, resultJSON []byte) error {
	return r.db.Model(&model.AITask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        constant.AITaskStatusCompleted,
		"progress":      100,
		"stage":         "completed",
		"stage_label":   "生成完成",
		"result_json":   datatypes.JSON(resultJSON),
		"error_message": nil,
		"completed_at":  gorm.Expr("NOW()"),
	}).Error
}

// SetFailed marks a task as failed with an error message.
func (r *AITaskRepository) SetFailed(id, errMsg string) error {
	return r.db.Model(&model.AITask{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        constant.AITaskStatusFailed,
		"stage":         "failed",
		"stage_label":   "任务失败",
		"error_message": errMsg,
		"completed_at":  gorm.Expr("NOW()"),
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

// FindByIDs retrieves multiple documents by their IDs in a single query.
func (r *DocumentRepository) FindByIDs(ids []string) ([]model.Document, error) {
	var docs []model.Document
	err := r.db.Where("id IN ?", ids).Find(&docs).Error
	return docs, err
}

func (r *DocumentRepository) DeleteByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.Document{}).Error
}
