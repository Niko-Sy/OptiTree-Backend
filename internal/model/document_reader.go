package model

import (
	"time"

	"gorm.io/datatypes"
)

type DocumentConversionTask struct {
	ID              string     `gorm:"primaryKey;column:id;size:32" json:"id"`
	DocumentID      string     `gorm:"column:document_id;size:32;not null" json:"documentId"`
	ProjectID       *string    `gorm:"column:project_id;size:32" json:"projectId,omitempty"`
	Status          string     `gorm:"column:status;size:20;not null;default:'pending'" json:"status"`
	AttemptCount    int        `gorm:"column:attempt_count;not null;default:0" json:"attemptCount"`
	MaxAttempts     int        `gorm:"column:max_attempts;not null;default:1" json:"maxAttempts"`
	ErrorMessage    *string    `gorm:"column:error_message" json:"errorMessage,omitempty"`
	DerivedPDFDocID *string    `gorm:"column:derived_pdf_doc_id;size:32" json:"derivedPdfDocId,omitempty"`
	StartedAt       *time.Time `gorm:"column:started_at" json:"startedAt,omitempty"`
	CompletedAt     *time.Time `gorm:"column:completed_at" json:"completedAt,omitempty"`
	CreatedAt       time.Time  `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt       time.Time  `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (DocumentConversionTask) TableName() string {
	return "document_conversion_tasks"
}

type DocumentSearchIndex struct {
	ID             string         `gorm:"primaryKey;column:id;size:32" json:"id"`
	DocumentID     string         `gorm:"column:document_id;size:32;not null" json:"documentId"`
	ProjectID      string         `gorm:"column:project_id;size:32;not null" json:"projectId"`
	ReaderKind     string         `gorm:"column:reader_kind;size:20;not null" json:"readerKind"`
	Snippet        string         `gorm:"column:snippet;not null" json:"snippet"`
	SearchableText string         `gorm:"column:searchable_text;not null" json:"searchableText"`
	LocatorJSON    datatypes.JSON `gorm:"column:locator_json;type:jsonb;not null" json:"locatorJson"`
	CreatedAt      time.Time      `gorm:"column:created_at;not null;autoCreateTime" json:"createdAt"`
	UpdatedAt      time.Time      `gorm:"column:updated_at;not null;autoUpdateTime" json:"updatedAt"`
}

func (DocumentSearchIndex) TableName() string {
	return "document_search_indexes"
}
