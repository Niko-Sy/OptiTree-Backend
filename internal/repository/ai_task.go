package repository

import (
	"errors"
	"strings"
	"time"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/util"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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

func (r *DocumentRepository) FindByProjectID(projectID string) ([]model.Document, error) {
	var docs []model.Document
	err := r.db.Where("project_id = ?", projectID).
		Order("uploaded_at DESC").
		Find(&docs).Error
	return docs, err
}

func (r *DocumentRepository) FindByProjectAndKeyword(projectID, keyword string) ([]model.Document, error) {
	query := r.db.Where("project_id = ?", projectID)
	trimmed := strings.TrimSpace(keyword)
	if trimmed != "" {
		like := "%" + trimmed + "%"
		query = query.Where("file_name ILIKE ? OR COALESCE(summary, '') ILIKE ?", like, like)
	}

	var docs []model.Document
	err := query.Order("uploaded_at DESC").Find(&docs).Error
	return docs, err
}

func (r *DocumentRepository) FindReadyWithoutSearchIndex(limit int) ([]model.Document, error) {
	if limit <= 0 {
		limit = 20
	}

	var docs []model.Document
	err := r.db.
		Where("project_id IS NOT NULL").
		Where("preview_status = ?", constant.DocumentPreviewReady).
		Where("reader_kind IN ?", []string{
			constant.DocumentReaderKindPDF,
			constant.DocumentReaderKindTabular,
			constant.DocumentReaderKindText,
		}).
		Where("NOT EXISTS (SELECT 1 FROM documents d2 WHERE d2.derived_pdf_doc_id = documents.id)").
		Where("NOT EXISTS (SELECT 1 FROM document_search_indexes dsi WHERE dsi.document_id = documents.id)").
		Order("uploaded_at DESC").
		Limit(limit).
		Find(&docs).Error
	return docs, err
}

func (r *DocumentRepository) UpdatePreviewMeta(
	docID string,
	previewStatus string,
	derivedPDFDocID *string,
	previewErrorMessage *string,
) error {
	updates := map[string]interface{}{
		"preview_status": previewStatus,
	}
	if derivedPDFDocID != nil {
		trimmedDerivedID := strings.TrimSpace(*derivedPDFDocID)
		if trimmedDerivedID == "" {
			updates["derived_pdf_doc_id"] = nil
		} else {
			updates["derived_pdf_doc_id"] = trimmedDerivedID
		}
	} else {
		updates["derived_pdf_doc_id"] = nil
	}
	if previewErrorMessage != nil {
		updates["preview_error_message"] = strings.TrimSpace(*previewErrorMessage)
	} else {
		updates["preview_error_message"] = nil
	}

	return r.db.Model(&model.Document{}).
		Where("id = ?", docID).
		Updates(updates).Error
}

func (r *DocumentRepository) DeleteByProject(tx *gorm.DB, projectID string) error {
	if tx == nil {
		tx = r.db
	}
	return tx.Where("project_id = ?", projectID).Delete(&model.Document{}).Error
}

type DocumentConversionTaskRepository struct {
	db *gorm.DB
}

func NewDocumentConversionTaskRepository(db *gorm.DB) *DocumentConversionTaskRepository {
	return &DocumentConversionTaskRepository{db: db}
}

func (r *DocumentConversionTaskRepository) UpsertPendingByDocument(documentID string, projectID *string) error {
	docID := strings.TrimSpace(documentID)
	if docID == "" {
		return errors.New("document id is required")
	}

	var normalizedProjectID *string
	if projectID != nil {
		trimmedProjectID := strings.TrimSpace(*projectID)
		if trimmedProjectID != "" {
			normalizedProjectID = &trimmedProjectID
		}
	}

	task := &model.DocumentConversionTask{
		ID:           util.NewDocumentConversionTaskID(),
		DocumentID:   docID,
		ProjectID:    normalizedProjectID,
		Status:       constant.DocumentConversionTaskPending,
		AttemptCount: 0,
		MaxAttempts:  1,
	}

	return r.db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "document_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"project_id":         normalizedProjectID,
			"status":             constant.DocumentConversionTaskPending,
			"attempt_count":      0,
			"error_message":      nil,
			"derived_pdf_doc_id": nil,
			"started_at":         nil,
			"completed_at":       nil,
			"updated_at":         gorm.Expr("NOW()"),
		}),
	}).Create(task).Error
}

func (r *DocumentConversionTaskRepository) TakeNextPending() (*model.DocumentConversionTask, error) {
	var task model.DocumentConversionTask
	err := r.db.Transaction(func(tx *gorm.DB) error {
		err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", constant.DocumentConversionTaskPending).
			Order("created_at ASC").
			First(&task).Error
		if err != nil {
			return err
		}

		attemptCount := task.AttemptCount + 1
		if err := tx.Model(&model.DocumentConversionTask{}).
			Where("id = ?", task.ID).
			Updates(map[string]interface{}{
				"status":        constant.DocumentConversionTaskProcessing,
				"attempt_count": attemptCount,
				"started_at":    gorm.Expr("COALESCE(started_at, NOW())"),
				"error_message": nil,
				"updated_at":    gorm.Expr("NOW()"),
			}).Error; err != nil {
			return err
		}

		task.Status = constant.DocumentConversionTaskProcessing
		task.AttemptCount = attemptCount
		if task.StartedAt == nil {
			now := time.Now().UTC()
			task.StartedAt = &now
		}
		return nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &task, nil
}

func (r *DocumentConversionTaskRepository) SetCompleted(taskID string, derivedPDFDocID *string) error {
	updates := map[string]interface{}{
		"status":        constant.DocumentConversionTaskCompleted,
		"error_message": nil,
		"completed_at":  gorm.Expr("NOW()"),
		"updated_at":    gorm.Expr("NOW()"),
	}

	if derivedPDFDocID != nil {
		trimmedDerivedID := strings.TrimSpace(*derivedPDFDocID)
		if trimmedDerivedID == "" {
			updates["derived_pdf_doc_id"] = nil
		} else {
			updates["derived_pdf_doc_id"] = trimmedDerivedID
		}
	} else {
		updates["derived_pdf_doc_id"] = nil
	}

	return r.db.Model(&model.DocumentConversionTask{}).
		Where("id = ?", strings.TrimSpace(taskID)).
		Updates(updates).Error
}

func (r *DocumentConversionTaskRepository) SetFailed(taskID string, errMsg string) error {
	message := strings.TrimSpace(errMsg)
	if message == "" {
		message = "DOCX conversion failed"
	}

	return r.db.Model(&model.DocumentConversionTask{}).
		Where("id = ?", strings.TrimSpace(taskID)).
		Updates(map[string]interface{}{
			"status":        constant.DocumentConversionTaskFailed,
			"error_message": message,
			"completed_at":  gorm.Expr("NOW()"),
			"updated_at":    gorm.Expr("NOW()"),
		}).Error
}

type DocumentSearchIndexRepository struct {
	db *gorm.DB
}

type DocumentSearchHit struct {
	IndexID         string         `gorm:"column:index_id"`
	DocumentID      string         `gorm:"column:document_id"`
	DocName         string         `gorm:"column:doc_name"`
	ReaderKind      string         `gorm:"column:reader_kind"`
	PreviewStatus   string         `gorm:"column:preview_status"`
	DerivedPDFDocID *string        `gorm:"column:derived_pdf_doc_id"`
	Snippet         string         `gorm:"column:snippet"`
	LocatorJSON     datatypes.JSON `gorm:"column:locator_json"`
}

func NewDocumentSearchIndexRepository(db *gorm.DB) *DocumentSearchIndexRepository {
	return &DocumentSearchIndexRepository{db: db}
}

func (r *DocumentSearchIndexRepository) ReplaceForDocument(documentID string, indexes []model.DocumentSearchIndex) error {
	docID := strings.TrimSpace(documentID)
	if docID == "" {
		return errors.New("document id is required")
	}

	return r.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("document_id = ?", docID).Delete(&model.DocumentSearchIndex{}).Error; err != nil {
			return err
		}
		if len(indexes) == 0 {
			return nil
		}
		return tx.CreateInBatches(indexes, 200).Error
	})
}

func (r *DocumentSearchIndexRepository) DeleteByDocumentID(documentID string) error {
	docID := strings.TrimSpace(documentID)
	if docID == "" {
		return nil
	}
	return r.db.Where("document_id = ?", docID).Delete(&model.DocumentSearchIndex{}).Error
}

func (r *DocumentSearchIndexRepository) SearchByProjectAndKeyword(projectID, keyword string, limit int) ([]DocumentSearchHit, error) {
	projectID = strings.TrimSpace(projectID)
	keyword = strings.TrimSpace(keyword)
	if projectID == "" || keyword == "" {
		return []DocumentSearchHit{}, nil
	}
	if limit <= 0 {
		limit = 100
	}

	likeKeyword := "%" + keyword + "%"
	hits := make([]DocumentSearchHit, 0)
	err := r.db.Table("document_search_indexes dsi").
		Select("dsi.id AS index_id, dsi.document_id, d.file_name AS doc_name, d.reader_kind, d.preview_status, d.derived_pdf_doc_id, dsi.snippet, dsi.locator_json").
		Joins("JOIN documents d ON d.id = dsi.document_id").
		Where("dsi.project_id = ?", projectID).
		Where("d.preview_status = ?", constant.DocumentPreviewReady).
		Where("dsi.searchable_text ILIKE ?", likeKeyword).
		Order("dsi.updated_at DESC").
		Limit(limit).
		Scan(&hits).Error
	return hits, err
}
