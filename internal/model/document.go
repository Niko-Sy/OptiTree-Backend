package model

import "time"

type Document struct {
	ID                  string    `gorm:"primaryKey;column:id;size:32" json:"id"`
	FileName            string    `gorm:"column:file_name;size:255;not null" json:"fileName"`
	FileType            string    `gorm:"column:file_type;size:10;not null" json:"fileType"`
	ReaderKind          string    `gorm:"column:reader_kind;size:20" json:"readerKind"`
	MimeType            string    `gorm:"column:mime_type;size:50;not null" json:"mimeType"`
	Size                int64     `gorm:"column:size;not null" json:"size"`
	Status              string    `gorm:"column:status;size:20;not null;default:'pending'" json:"status"`
	PreviewStatus       string    `gorm:"column:preview_status;size:20" json:"previewStatus"`
	DerivedPdfDocID     *string   `gorm:"column:derived_pdf_doc_id;size:32" json:"derivedPdfDocId,omitempty"`
	PreviewErrorMessage *string   `gorm:"column:preview_error_message" json:"previewErrorMessage,omitempty"`
	Summary             *string   `gorm:"column:summary" json:"summary"`
	SourceURL           string    `gorm:"column:source_url;not null" json:"sourceUrl"`
	TextExtractURL      *string   `gorm:"column:text_extract_url" json:"textExtractUrl"`
	UploadedAt          time.Time `gorm:"column:uploaded_at;not null;autoCreateTime" json:"uploadedAt"`
	UploadedBy          string    `gorm:"column:uploaded_by;size:32;not null" json:"uploadedBy"`
	ProjectID           *string   `gorm:"column:project_id;size:32" json:"projectId"`
}

func (Document) TableName() string {
	return "documents"
}
