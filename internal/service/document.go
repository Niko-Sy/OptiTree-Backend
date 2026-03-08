package service

import (
	"context"
	"errors"
	"mime/multipart"
	"path/filepath"
	"strings"

	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"
)

var ErrDocumentNotFound = errors.New("文档不存在")

var mimeToFileType = map[string]string{
	"application/pdf":    "pdf",
	"application/msword": "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.ms-excel": "xlsx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	"text/plain": "txt",
}

type DocumentService struct {
	docRepo *repository.DocumentRepository
	storage *StorageService
}

func NewDocumentService(docRepo *repository.DocumentRepository, storage *StorageService) *DocumentService {
	return &DocumentService{docRepo: docRepo, storage: storage}
}

type UploadDocumentInput struct {
	File       multipart.File
	Header     *multipart.FileHeader
	MimeType   string
	UploadedBy string
	ProjectID  *string
}

func (s *DocumentService) Upload(ctx context.Context, input UploadDocumentInput) (*model.Document, error) {
	url, err := s.storage.SaveDocument(input.File, input.Header, input.MimeType)
	if err != nil {
		return nil, err
	}

	fileType := mimeToFileType[input.MimeType]
	if fileType == "" {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(input.Header.Filename)), ".")
		fileType = ext
	}

	doc := &model.Document{
		ID:         util.NewDocumentID(),
		FileName:   input.Header.Filename,
		FileType:   fileType,
		MimeType:   input.MimeType,
		Size:       input.Header.Size,
		Status:     "pending",
		SourceURL:  url,
		UploadedBy: input.UploadedBy,
		ProjectID:  input.ProjectID,
	}

	if err := s.docRepo.Create(doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func (s *DocumentService) GetByID(ctx context.Context, id string) (*model.Document, error) {
	doc, err := s.docRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}
	return doc, nil
}
