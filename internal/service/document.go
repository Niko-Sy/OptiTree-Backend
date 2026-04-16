package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/xuri/excelize/v2"
	"gorm.io/datatypes"
	rscpdf "rsc.io/pdf"
)

var (
	ErrDocumentNotFound          = errors.New("文档不存在")
	ErrDocumentPermissionDenied  = errors.New("无权访问文档")
	ErrDocumentPreviewProcessing = errors.New("文档预览生成中")
	ErrDocumentPreviewFailed     = errors.New("文档预览不可用")
)

const documentSearchMaxReadBytes int64 = 32 * 1024 * 1024

const (
	documentSearchQueryLimit       = 200
	documentSearchCacheTTL         = 10 * time.Minute
	documentSearchCacheIndexTTL    = 12 * time.Hour
	documentWorkerPollInterval     = 2 * time.Second
	documentIndexMaxBlocks         = 300
	documentDefaultConvertWorkers  = 1
	documentDefaultSearchIxWorkers = 1
)

var mimeToFileType = map[string]string{
	"application/pdf":    "pdf",
	"application/msword": "doc",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document": "docx",
	"application/vnd.ms-excel": "xlsx",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet": "xlsx",
	"text/csv":                  "csv",
	"text/tab-separated-values": "tsv",
	"text/plain":                "txt",
	"application/json":          "json",
	"application/xml":           "xml",
	"text/xml":                  "xml",
	"application/x-yaml":        "yaml",
	"text/yaml":                 "yaml",
}

type DocumentBinary struct {
	Reader      io.ReadCloser
	Size        int64
	ContentType string
	FileName    string
}

type ProjectDocumentListItem struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Ext             string  `json:"ext"`
	MimeType        string  `json:"mimeType"`
	ReaderKind      string  `json:"readerKind"`
	PreviewStatus   string  `json:"previewStatus"`
	DerivedPdfDocID *string `json:"derivedPdfDocId,omitempty"`
	Size            int64   `json:"size"`
	UploadedAt      string  `json:"uploadedAt"`
}

type DocumentSearchLocator struct {
	Type        string `json:"type"`
	Keyword     string `json:"keyword,omitempty"`
	Page        int    `json:"page,omitempty"`
	MatchIndex  int    `json:"matchIndex,omitempty"`
	SheetName   string `json:"sheetName,omitempty"`
	RowIndex    int    `json:"rowIndex,omitempty"`
	ColIndex    int    `json:"colIndex,omitempty"`
	BlockIndex  int    `json:"blockIndex,omitempty"`
	LineStart   int    `json:"lineStart,omitempty"`
	LineEnd     int    `json:"lineEnd,omitempty"`
	StartOffset int    `json:"startOffset,omitempty"`
	EndOffset   int    `json:"endOffset,omitempty"`
}

type DocumentSearchResult struct {
	ID              string                `json:"id"`
	DocID           string                `json:"docId"`
	DocName         string                `json:"docName"`
	ReaderKind      string                `json:"readerKind"`
	PreviewStatus   string                `json:"previewStatus"`
	DerivedPdfDocID *string               `json:"derivedPdfDocId,omitempty"`
	Snippet         string                `json:"snippet"`
	Keyword         string                `json:"keyword"`
	Locator         DocumentSearchLocator `json:"locator"`
}

type DocumentService struct {
	docRepo            *repository.DocumentRepository
	docConvertTaskRepo *repository.DocumentConversionTaskRepository
	docSearchIndexRepo *repository.DocumentSearchIndexRepository
	memberRepo         *repository.MemberRepository
	storage            *StorageService
	rdb                *redis.Client

	workerMu          sync.Mutex
	workerCtx         context.Context
	workerCancel      context.CancelFunc
	workerWG          sync.WaitGroup
	workerStarted     bool
	convertWorkers    int
	searchIndexWorker int
	pollInterval      time.Duration
}

func NewDocumentService(
	docRepo *repository.DocumentRepository,
	docConvertTaskRepo *repository.DocumentConversionTaskRepository,
	docSearchIndexRepo *repository.DocumentSearchIndexRepository,
	memberRepo *repository.MemberRepository,
	storage *StorageService,
	rdb *redis.Client,
) *DocumentService {
	return &DocumentService{
		docRepo:            docRepo,
		docConvertTaskRepo: docConvertTaskRepo,
		docSearchIndexRepo: docSearchIndexRepo,
		memberRepo:         memberRepo,
		storage:            storage,
		rdb:                rdb,
		convertWorkers:     documentDefaultConvertWorkers,
		searchIndexWorker:  documentDefaultSearchIxWorkers,
		pollInterval:       documentWorkerPollInterval,
	}
}

func (s *DocumentService) StartBackgroundWorkers(ctx context.Context) error {
	if s.docConvertTaskRepo == nil && s.docSearchIndexRepo == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	s.workerMu.Lock()
	defer s.workerMu.Unlock()
	if s.workerStarted {
		return nil
	}

	workerCtx, cancel := context.WithCancel(ctx)
	s.workerCtx = workerCtx
	s.workerCancel = cancel
	s.workerStarted = true

	if s.docConvertTaskRepo != nil {
		workers := s.convertWorkers
		if workers <= 0 {
			workers = documentDefaultConvertWorkers
		}
		for idx := 0; idx < workers; idx++ {
			workerName := fmt.Sprintf("doc-convert-%d", idx+1)
			s.workerWG.Add(1)
			go func(name string) {
				defer s.workerWG.Done()
				s.conversionWorkerLoop(workerCtx, name)
			}(workerName)
		}
	}

	if s.docSearchIndexRepo != nil {
		workers := s.searchIndexWorker
		if workers <= 0 {
			workers = documentDefaultSearchIxWorkers
		}
		for idx := 0; idx < workers; idx++ {
			workerName := fmt.Sprintf("doc-index-%d", idx+1)
			s.workerWG.Add(1)
			go func(name string) {
				defer s.workerWG.Done()
				s.searchIndexWorkerLoop(workerCtx, name)
			}(workerName)
		}
	}

	log.Info().Msg("document background workers started")
	return nil
}

func (s *DocumentService) StopBackgroundWorkers() {
	s.workerMu.Lock()
	if !s.workerStarted {
		s.workerMu.Unlock()
		return
	}
	cancel := s.workerCancel
	s.workerMu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.workerWG.Wait()

	s.workerMu.Lock()
	s.workerStarted = false
	s.workerCancel = nil
	s.workerCtx = nil
	s.workerMu.Unlock()
}

func (s *DocumentService) conversionWorkerLoop(ctx context.Context, workerName string) {
	for {
		if ctx.Err() != nil {
			return
		}

		task, err := s.docConvertTaskRepo.TakeNextPending()
		if err != nil {
			log.Warn().Err(err).Str("worker", workerName).Msg("take pending document conversion task failed")
			if s.waitOrDone(ctx, s.pollInterval) {
				return
			}
			continue
		}
		if task == nil {
			if s.waitOrDone(ctx, s.pollInterval) {
				return
			}
			continue
		}

		s.handleConversionTask(ctx, task, workerName)
	}
}

func (s *DocumentService) searchIndexWorkerLoop(ctx context.Context, workerName string) {
	for {
		if ctx.Err() != nil {
			return
		}

		docs, err := s.docRepo.FindReadyWithoutSearchIndex(20)
		if err != nil {
			log.Warn().Err(err).Str("worker", workerName).Msg("find pending document search indexes failed")
			if s.waitOrDone(ctx, s.pollInterval) {
				return
			}
			continue
		}
		if len(docs) == 0 {
			if s.waitOrDone(ctx, s.pollInterval) {
				return
			}
			continue
		}

		for i := range docs {
			if ctx.Err() != nil {
				return
			}
			s.buildAndReplaceDocumentSearchIndex(ctx, &docs[i], workerName)
		}
	}
}

func (s *DocumentService) waitOrDone(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		duration = documentWorkerPollInterval
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
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

	fileType := inferFileType(input.Header.Filename, input.MimeType)
	readerKind := inferReaderKind(fileType, input.MimeType, input.Header.Filename)
	previewStatus := inferPreviewStatus(fileType, readerKind)

	doc := &model.Document{
		ID:            util.NewDocumentID(),
		FileName:      input.Header.Filename,
		FileType:      fileType,
		ReaderKind:    readerKind,
		MimeType:      input.MimeType,
		Size:          input.Header.Size,
		Status:        constant.DocStatusPending,
		PreviewStatus: previewStatus,
		SourceURL:     url,
		UploadedBy:    input.UploadedBy,
		ProjectID:     input.ProjectID,
	}

	if err := s.docRepo.Create(doc); err != nil {
		return nil, err
	}

	if strings.EqualFold(strings.TrimSpace(fileType), "docx") && s.docConvertTaskRepo != nil {
		if err := s.docConvertTaskRepo.UpsertPendingByDocument(doc.ID, doc.ProjectID); err != nil {
			errMsg := "DOCX 异步转换任务创建失败"
			_ = s.docRepo.UpdatePreviewMeta(doc.ID, constant.DocumentPreviewFailed, nil, &errMsg)
			doc.PreviewStatus = constant.DocumentPreviewFailed
			doc.PreviewErrorMessage = &errMsg
			log.Warn().Err(err).Str("docId", doc.ID).Msg("enqueue DOCX conversion task failed")
		}
	}
	return doc, nil
}

func (s *DocumentService) ListByProject(ctx context.Context, projectID string, userID string) ([]ProjectDocumentListItem, error) {
	if err := s.ensureProjectViewer(projectID, userID); err != nil {
		return nil, err
	}

	docs, err := s.docRepo.FindByProjectID(projectID)
	if err != nil {
		return nil, err
	}

	list := make([]ProjectDocumentListItem, 0, len(docs))
	for i := range docs {
		doc := docs[i]
		readerKind := effectiveReaderKind(doc)
		previewStatus := effectivePreviewStatus(doc, readerKind)
		list = append(list, ProjectDocumentListItem{
			ID:              doc.ID,
			Name:            doc.FileName,
			Ext:             inferExt(doc.FileName, doc.FileType),
			MimeType:        doc.MimeType,
			ReaderKind:      readerKind,
			PreviewStatus:   previewStatus,
			DerivedPdfDocID: trimStringPointer(doc.DerivedPdfDocID),
			Size:            doc.Size,
			UploadedAt:      doc.UploadedAt.UTC().Format("2006-01-02T15:04:05Z"),
		})
	}
	return list, nil
}

func (s *DocumentService) Search(ctx context.Context, projectID string, keyword string, userID string) ([]DocumentSearchResult, error) {
	if err := s.ensureProjectViewer(projectID, userID); err != nil {
		return nil, err
	}

	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []DocumentSearchResult{}, nil
	}
	projectID = strings.TrimSpace(projectID)

	if s.docSearchIndexRepo == nil {
		return s.searchOnline(ctx, projectID, keyword)
	}

	cacheKey := buildDocumentSearchCacheKey(projectID, keyword)
	if cached, ok := s.getSearchCache(ctx, cacheKey); ok {
		return cached, nil
	}

	hits, err := s.docSearchIndexRepo.SearchByProjectAndKeyword(projectID, keyword, documentSearchQueryLimit)
	if err != nil {
		return nil, err
	}

	results := make([]DocumentSearchResult, 0, len(hits))
	for i := range hits {
		hit := hits[i]
		locator := DocumentSearchLocator{}
		if len(hit.LocatorJSON) > 0 {
			if unmarshalErr := json.Unmarshal(hit.LocatorJSON, &locator); unmarshalErr != nil {
				continue
			}
		}
		results = append(results, DocumentSearchResult{
			ID:              strings.TrimSpace(hit.IndexID),
			DocID:           strings.TrimSpace(hit.DocumentID),
			DocName:         hit.DocName,
			ReaderKind:      hit.ReaderKind,
			PreviewStatus:   hit.PreviewStatus,
			DerivedPdfDocID: trimStringPointer(hit.DerivedPDFDocID),
			Snippet:         hit.Snippet,
			Keyword:         keyword,
			Locator:         locator,
		})
	}

	if len(results) == 0 {
		results = []DocumentSearchResult{}
	}
	s.setSearchCache(ctx, projectID, cacheKey, results)
	return results, nil

}

func (s *DocumentService) searchOnline(ctx context.Context, projectID string, keyword string) ([]DocumentSearchResult, error) {

	docs, err := s.docRepo.FindByProjectID(projectID)
	if err != nil {
		return nil, err
	}

	results := make([]DocumentSearchResult, 0)
	for i := range docs {
		doc := docs[i]
		readerKind := effectiveReaderKind(doc)
		if effectivePreviewStatus(doc, readerKind) != constant.DocumentPreviewReady {
			continue
		}

		locator, snippet, ok := s.searchDocument(ctx, &doc, keyword, readerKind)
		if !ok {
			continue
		}

		results = append(results, DocumentSearchResult{
			ID:              util.NewID("hit"),
			DocID:           doc.ID,
			DocName:         doc.FileName,
			ReaderKind:      readerKind,
			PreviewStatus:   effectivePreviewStatus(doc, readerKind),
			DerivedPdfDocID: trimStringPointer(doc.DerivedPdfDocID),
			Snippet:         snippet,
			Keyword:         keyword,
			Locator:         locator,
		})
	}
	return results, nil
}

func (s *DocumentService) Preview(ctx context.Context, docID string, userID string) (*DocumentBinary, error) {
	doc, err := s.GetByID(ctx, docID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentAccess(doc, userID); err != nil {
		return nil, err
	}

	readerKind := effectiveReaderKind(*doc)
	previewStatus := effectivePreviewStatus(*doc, readerKind)
	switch previewStatus {
	case constant.DocumentPreviewProcessing:
		return nil, ErrDocumentPreviewProcessing
	case constant.DocumentPreviewFailed:
		return nil, ErrDocumentPreviewFailed
	}

	targetDoc, err := s.resolvePreviewTarget(doc)
	if err != nil {
		return nil, err
	}

	binary, err := s.openDocumentBinary(ctx, targetDoc)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(binary.FileName) == "" {
		binary.FileName = doc.FileName
	}
	return binary, nil
}

func (s *DocumentService) Download(ctx context.Context, docID string, userID string) (*DocumentBinary, error) {
	doc, err := s.GetByID(ctx, docID)
	if err != nil {
		return nil, err
	}
	if err := s.ensureDocumentAccess(doc, userID); err != nil {
		return nil, err
	}

	binary, err := s.openDocumentBinary(ctx, doc)
	if err == nil {
		return binary, nil
	}
	if err != ErrDocumentNotFound {
		return nil, err
	}

	derivedID := trimStringPointer(doc.DerivedPdfDocID)
	if derivedID == nil {
		return nil, ErrDocumentNotFound
	}
	derivedDoc, findErr := s.docRepo.FindByID(*derivedID)
	if findErr != nil {
		return nil, findErr
	}
	if derivedDoc == nil {
		return nil, ErrDocumentNotFound
	}
	return s.openDocumentBinary(ctx, derivedDoc)
}

func (s *DocumentService) GetByID(ctx context.Context, id string) (*model.Document, error) {
	doc, err := s.docRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	if strings.TrimSpace(doc.ReaderKind) == "" {
		doc.ReaderKind = inferReaderKind(doc.FileType, doc.MimeType, doc.FileName)
	}
	if strings.TrimSpace(doc.PreviewStatus) == "" {
		doc.PreviewStatus = inferPreviewStatus(doc.FileType, doc.ReaderKind)
	}
	return doc, nil
}

func (s *DocumentService) ensureProjectViewer(projectID string, userID string) error {
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	if projectID == "" || userID == "" {
		return ErrDocumentPermissionDenied
	}
	member, err := s.memberRepo.FindByProjectAndUser(projectID, userID)
	if err != nil {
		return err
	}
	if member == nil {
		return ErrDocumentPermissionDenied
	}
	return nil
}

func (s *DocumentService) ensureDocumentAccess(doc *model.Document, userID string) error {
	if doc == nil {
		return ErrDocumentNotFound
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ErrDocumentPermissionDenied
	}

	if doc.ProjectID != nil && strings.TrimSpace(*doc.ProjectID) != "" {
		return s.ensureProjectViewer(strings.TrimSpace(*doc.ProjectID), userID)
	}
	if strings.TrimSpace(doc.UploadedBy) != userID {
		return ErrDocumentPermissionDenied
	}
	return nil
}

func (s *DocumentService) resolvePreviewTarget(doc *model.Document) (*model.Document, error) {
	if doc == nil {
		return nil, ErrDocumentNotFound
	}

	if strings.EqualFold(strings.TrimSpace(doc.FileType), "docx") {
		derivedID := trimStringPointer(doc.DerivedPdfDocID)
		if derivedID == nil {
			return nil, ErrDocumentPreviewFailed
		}
		derivedDoc, err := s.docRepo.FindByID(*derivedID)
		if err != nil {
			return nil, err
		}
		if derivedDoc == nil {
			return nil, ErrDocumentPreviewFailed
		}
		return derivedDoc, nil
	}

	return doc, nil
}

func (s *DocumentService) openDocumentBinary(ctx context.Context, doc *model.Document) (*DocumentBinary, error) {
	if doc == nil {
		return nil, ErrDocumentNotFound
	}
	rc, size, contentType, err := s.storage.OpenDocument(ctx, doc.SourceURL, doc.MimeType)
	if err != nil {
		if errors.Is(err, ErrStorageNotFound) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}

	return &DocumentBinary{
		Reader:      rc,
		Size:        size,
		ContentType: contentType,
		FileName:    doc.FileName,
	}, nil
}

func (s *DocumentService) searchDocument(
	ctx context.Context,
	doc *model.Document,
	keyword string,
	readerKind string,
) (DocumentSearchLocator, string, bool) {
	targetDoc, err := s.resolveSearchTarget(doc)
	if err != nil || targetDoc == nil {
		return DocumentSearchLocator{}, "", false
	}

	data, err := s.readDocumentBytes(ctx, targetDoc)
	if err != nil || len(data) == 0 {
		return DocumentSearchLocator{}, "", false
	}

	switch readerKind {
	case constant.DocumentReaderKindText:
		return searchTextContent(string(data), keyword)
	case constant.DocumentReaderKindTabular:
		ext := inferExt(targetDoc.FileName, targetDoc.FileType)
		return searchTabularContent(data, keyword, ext)
	case constant.DocumentReaderKindPDF:
		return searchPDFContent(data, keyword)
	default:
		return DocumentSearchLocator{}, "", false
	}
}

func (s *DocumentService) resolveSearchTarget(doc *model.Document) (*model.Document, error) {
	if doc == nil {
		return nil, ErrDocumentNotFound
	}
	if strings.EqualFold(doc.FileType, "docx") {
		return s.resolvePreviewTarget(doc)
	}
	return doc, nil
}

func (s *DocumentService) readDocumentBytes(ctx context.Context, doc *model.Document) ([]byte, error) {
	rc, _, _, err := s.storage.OpenDocument(ctx, doc.SourceURL, doc.MimeType)
	if err != nil {
		if errors.Is(err, ErrStorageNotFound) {
			return nil, ErrDocumentNotFound
		}
		return nil, err
	}
	defer rc.Close()

	limited := io.LimitReader(rc, documentSearchMaxReadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > documentSearchMaxReadBytes {
		return nil, ErrFileTooLarge
	}
	return data, nil
}

type documentSearchIndexBlock struct {
	SearchableText string
	Snippet        string
	Locator        DocumentSearchLocator
}

func buildDocumentSearchCacheKey(projectID, keyword string) string {
	projectID = strings.TrimSpace(projectID)
	normalizedKeyword := strings.ToLower(strings.TrimSpace(keyword))
	if normalizedKeyword == "" {
		normalizedKeyword = "none"
	}
	hash := util.SHA256(normalizedKeyword)
	if len(hash) > 16 {
		hash = hash[:16]
	}
	return constant.RedisKeyDocumentSearch + projectID + ":q:" + hash
}

func documentSearchCacheIndexKey(projectID string) string {
	return constant.RedisKeyDocumentSearchIx + strings.TrimSpace(projectID)
}

func (s *DocumentService) getSearchCache(ctx context.Context, cacheKey string) ([]DocumentSearchResult, bool) {
	if s.rdb == nil || strings.TrimSpace(cacheKey) == "" {
		return nil, false
	}

	payload, err := s.rdb.Get(ctx, cacheKey).Result()
	if err != nil || strings.TrimSpace(payload) == "" {
		return nil, false
	}

	results := make([]DocumentSearchResult, 0)
	if err := json.Unmarshal([]byte(payload), &results); err != nil {
		_ = s.rdb.Del(ctx, cacheKey).Err()
		return nil, false
	}
	return results, true
}

func (s *DocumentService) setSearchCache(ctx context.Context, projectID, cacheKey string, results []DocumentSearchResult) {
	if s.rdb == nil || strings.TrimSpace(cacheKey) == "" {
		return
	}
	payload, err := json.Marshal(results)
	if err != nil {
		return
	}

	if err := s.rdb.Set(ctx, cacheKey, payload, documentSearchCacheTTL).Err(); err != nil {
		return
	}

	indexKey := documentSearchCacheIndexKey(projectID)
	if strings.TrimSpace(indexKey) == "" {
		return
	}
	_ = s.rdb.SAdd(ctx, indexKey, cacheKey).Err()
	_ = s.rdb.Expire(ctx, indexKey, documentSearchCacheIndexTTL).Err()
}

func (s *DocumentService) invalidateProjectSearchCache(ctx context.Context, projectID string) {
	if s.rdb == nil {
		return
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}

	indexKey := documentSearchCacheIndexKey(projectID)
	keys, err := s.rdb.SMembers(ctx, indexKey).Result()
	if err == nil && len(keys) > 0 {
		_ = s.rdb.Del(ctx, keys...).Err()
	}
	_ = s.rdb.Del(ctx, indexKey).Err()
}

func (s *DocumentService) handleConversionTask(ctx context.Context, task *model.DocumentConversionTask, workerName string) {
	if task == nil || s.docConvertTaskRepo == nil {
		return
	}

	sourceDoc, err := s.docRepo.FindByID(strings.TrimSpace(task.DocumentID))
	if err != nil {
		s.failConversionTask(ctx, task, nil, err, workerName)
		return
	}
	if sourceDoc == nil {
		s.failConversionTask(ctx, task, nil, errors.New("source document not found"), workerName)
		return
	}

	if !strings.EqualFold(strings.TrimSpace(sourceDoc.FileType), "docx") {
		_ = s.docRepo.UpdatePreviewMeta(sourceDoc.ID, constant.DocumentPreviewReady, nil, nil)
		_ = s.docConvertTaskRepo.SetCompleted(task.ID, nil)
		return
	}

	sourceReader, _, _, err := s.storage.OpenDocument(ctx, sourceDoc.SourceURL, sourceDoc.MimeType)
	if err != nil {
		s.failConversionTask(ctx, task, sourceDoc, err, workerName)
		return
	}
	_ = sourceReader.Close()

	placeholderPDF := buildDOCXPlaceholderPDF(sourceDoc.FileName)
	derivedFileName := replaceFileExt(sourceDoc.FileName, ".pdf")
	derivedSourceURL, err := s.storage.SaveGeneratedDocument(derivedFileName, "application/pdf", placeholderPDF)
	if err != nil {
		s.failConversionTask(ctx, task, sourceDoc, err, workerName)
		return
	}

	derivedDoc := &model.Document{
		ID:            util.NewDocumentID(),
		FileName:      derivedFileName,
		FileType:      "pdf",
		ReaderKind:    constant.DocumentReaderKindPDF,
		MimeType:      "application/pdf",
		Size:          int64(len(placeholderPDF)),
		Status:        constant.DocStatusParsed,
		PreviewStatus: constant.DocumentPreviewReady,
		SourceURL:     derivedSourceURL,
		UploadedBy:    sourceDoc.UploadedBy,
		ProjectID:     sourceDoc.ProjectID,
	}
	if err := s.docRepo.Create(derivedDoc); err != nil {
		s.failConversionTask(ctx, task, sourceDoc, err, workerName)
		return
	}

	derivedID := derivedDoc.ID
	if err := s.docRepo.UpdatePreviewMeta(sourceDoc.ID, constant.DocumentPreviewReady, &derivedID, nil); err != nil {
		s.failConversionTask(ctx, task, sourceDoc, err, workerName)
		return
	}

	if err := s.docConvertTaskRepo.SetCompleted(task.ID, &derivedID); err != nil {
		log.Warn().Err(err).Str("taskId", task.ID).Str("worker", workerName).Msg("set conversion task completed failed")
	}

	if sourceDoc.ProjectID != nil {
		s.invalidateProjectSearchCache(ctx, *sourceDoc.ProjectID)
	}

	log.Info().Str("taskId", task.ID).Str("docId", sourceDoc.ID).Str("worker", workerName).Msg("DOCX conversion placeholder completed")
}

func (s *DocumentService) failConversionTask(
	ctx context.Context,
	task *model.DocumentConversionTask,
	sourceDoc *model.Document,
	procErr error,
	workerName string,
) {
	if task == nil || s.docConvertTaskRepo == nil {
		return
	}

	errMsg := "DOCX conversion failed"
	if procErr != nil {
		errMsg = strings.TrimSpace(procErr.Error())
	}
	if errMsg == "" {
		errMsg = "DOCX conversion failed"
	}
	msgRunes := []rune(errMsg)
	if len(msgRunes) > 300 {
		errMsg = string(msgRunes[:300])
	}

	if sourceDoc != nil {
		_ = s.docRepo.UpdatePreviewMeta(sourceDoc.ID, constant.DocumentPreviewFailed, nil, &errMsg)
		if sourceDoc.ProjectID != nil {
			s.invalidateProjectSearchCache(ctx, *sourceDoc.ProjectID)
		}
	}
	_ = s.docConvertTaskRepo.SetFailed(task.ID, errMsg)

	log.Warn().
		Err(procErr).
		Str("taskId", task.ID).
		Str("docId", strings.TrimSpace(task.DocumentID)).
		Str("worker", workerName).
		Msg("DOCX conversion placeholder failed")
}

func buildDOCXPlaceholderPDF(sourceFileName string) []byte {
	sourceLabel := escapePDFText(toASCIISafe(sourceFileName))
	if sourceLabel == "" {
		sourceLabel = "unknown.docx"
	}

	streamText := "BT\n/F1 18 Tf\n72 770 Td\n(DOCX preview placeholder) Tj\n0 -26 Td\n/F1 12 Tf\n(Source file: " + sourceLabel + ") Tj\nET\n"

	var builder strings.Builder
	builder.WriteString("%PDF-1.4\n")

	offsets := make([]int, 6)
	writeObject := func(index int, body string) {
		offsets[index] = builder.Len()
		builder.WriteString(strconv.Itoa(index))
		builder.WriteString(" 0 obj\n")
		builder.WriteString(body)
		builder.WriteString("\nendobj\n")
	}

	writeObject(1, "<< /Type /Catalog /Pages 2 0 R >>")
	writeObject(2, "<< /Type /Pages /Kids [3 0 R] /Count 1 >>")
	writeObject(3, "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 4 0 R >> >> /Contents 5 0 R >>")
	writeObject(4, "<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>")
	writeObject(5, "<< /Length "+strconv.Itoa(len(streamText))+" >>\nstream\n"+streamText+"endstream")

	xrefStart := builder.Len()
	builder.WriteString("xref\n0 6\n")
	builder.WriteString("0000000000 65535 f \n")
	for index := 1; index <= 5; index++ {
		builder.WriteString(fmt.Sprintf("%010d 00000 n \n", offsets[index]))
	}
	builder.WriteString("trailer\n<< /Size 6 /Root 1 0 R >>\nstartxref\n")
	builder.WriteString(strconv.Itoa(xrefStart))
	builder.WriteString("\n%%EOF\n")

	return []byte(builder.String())
}

func toASCIISafe(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var builder strings.Builder
	for _, char := range text {
		if char >= 32 && char <= 126 {
			builder.WriteRune(char)
			continue
		}
		builder.WriteRune('?')
	}
	return strings.TrimSpace(builder.String())
}

func escapePDFText(text string) string {
	text = strings.ReplaceAll(text, "\\", "\\\\")
	text = strings.ReplaceAll(text, "(", "\\(")
	text = strings.ReplaceAll(text, ")", "\\)")
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return text
}

func replaceFileExt(fileName string, newExt string) string {
	baseName := strings.TrimSpace(fileName)
	if baseName == "" {
		baseName = "document"
	}
	ext := filepath.Ext(baseName)
	if ext != "" {
		baseName = strings.TrimSuffix(baseName, ext)
	}
	return baseName + newExt
}

func (s *DocumentService) buildAndReplaceDocumentSearchIndex(ctx context.Context, doc *model.Document, workerName string) {
	if doc == nil || s.docSearchIndexRepo == nil {
		return
	}

	indexes, err := s.buildDocumentSearchIndexes(ctx, doc)
	if err != nil {
		log.Warn().Err(err).Str("docId", doc.ID).Str("worker", workerName).Msg("build document search indexes failed")
		return
	}

	if err := s.docSearchIndexRepo.ReplaceForDocument(doc.ID, indexes); err != nil {
		log.Warn().Err(err).Str("docId", doc.ID).Str("worker", workerName).Msg("replace document search indexes failed")
		return
	}

	if doc.ProjectID != nil {
		s.invalidateProjectSearchCache(ctx, *doc.ProjectID)
	}
}

func (s *DocumentService) buildDocumentSearchIndexes(ctx context.Context, doc *model.Document) ([]model.DocumentSearchIndex, error) {
	if doc == nil {
		return nil, nil
	}
	if doc.ProjectID == nil || strings.TrimSpace(*doc.ProjectID) == "" {
		return nil, nil
	}
	projectID := strings.TrimSpace(*doc.ProjectID)

	readerKind := effectiveReaderKind(*doc)
	targetDoc, err := s.resolveSearchTarget(doc)
	if err != nil || targetDoc == nil {
		return []model.DocumentSearchIndex{s.buildEmptyDocumentSearchIndex(doc, projectID, readerKind)}, nil
	}

	data, err := s.readDocumentBytes(ctx, targetDoc)
	if err != nil || len(data) == 0 {
		return []model.DocumentSearchIndex{s.buildEmptyDocumentSearchIndex(doc, projectID, readerKind)}, nil
	}

	var blocks []documentSearchIndexBlock
	switch readerKind {
	case constant.DocumentReaderKindText:
		blocks = extractTextIndexBlocks(string(data))
	case constant.DocumentReaderKindTabular:
		blocks = extractTabularIndexBlocks(data, inferExt(targetDoc.FileName, targetDoc.FileType))
	case constant.DocumentReaderKindPDF:
		blocks = extractPDFIndexBlocks(data)
	default:
		blocks = []documentSearchIndexBlock{}
	}

	indexes := make([]model.DocumentSearchIndex, 0, len(blocks))
	for i := range blocks {
		block := blocks[i]
		searchable := strings.TrimSpace(block.SearchableText)
		if searchable == "" {
			continue
		}

		locatorBytes, marshalErr := json.Marshal(block.Locator)
		if marshalErr != nil {
			continue
		}

		snippet := strings.TrimSpace(block.Snippet)
		if snippet == "" {
			snippet = truncateRunes(searchable, 120)
		}

		indexes = append(indexes, model.DocumentSearchIndex{
			ID:             util.NewDocumentSearchIndexID(),
			DocumentID:     doc.ID,
			ProjectID:      projectID,
			ReaderKind:     readerKind,
			Snippet:        snippet,
			SearchableText: searchable,
			LocatorJSON:    datatypes.JSON(locatorBytes),
		})
	}

	if len(indexes) == 0 {
		return []model.DocumentSearchIndex{s.buildEmptyDocumentSearchIndex(doc, projectID, readerKind)}, nil
	}
	return indexes, nil
}

func (s *DocumentService) buildEmptyDocumentSearchIndex(doc *model.Document, projectID, readerKind string) model.DocumentSearchIndex {
	if strings.TrimSpace(readerKind) == "" {
		readerKind = constant.DocumentReaderKindText
	}
	locatorBytes, _ := json.Marshal(DocumentSearchLocator{Type: readerKind})
	return model.DocumentSearchIndex{
		ID:             util.NewDocumentSearchIndexID(),
		DocumentID:     doc.ID,
		ProjectID:      projectID,
		ReaderKind:     readerKind,
		Snippet:        "",
		SearchableText: "",
		LocatorJSON:    datatypes.JSON(locatorBytes),
	}
}

func extractTextIndexBlocks(content string) []documentSearchIndexBlock {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")

	blocks := make([]documentSearchIndexBlock, 0)
	for idx, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}
		lineRuneLen := utf8.RuneCountInString(trimmedLine)
		blocks = append(blocks, documentSearchIndexBlock{
			SearchableText: trimmedLine,
			Snippet:        truncateRunes(trimmedLine, 120),
			Locator: DocumentSearchLocator{
				Type:        constant.DocumentReaderKindText,
				BlockIndex:  idx,
				LineStart:   idx,
				LineEnd:     idx,
				StartOffset: 0,
				EndOffset:   lineRuneLen,
			},
		})
		if len(blocks) >= documentIndexMaxBlocks {
			return blocks
		}
	}
	return blocks
}

func extractTabularIndexBlocks(data []byte, ext string) []documentSearchIndexBlock {
	blocks := make([]documentSearchIndexBlock, 0)
	ext = strings.ToLower(strings.TrimSpace(ext))

	appendCell := func(cellValue string, sheetName string, rowIndex, colIndex int) {
		trimmed := strings.TrimSpace(cellValue)
		if trimmed == "" {
			return
		}
		runeLen := utf8.RuneCountInString(trimmed)
		blocks = append(blocks, documentSearchIndexBlock{
			SearchableText: trimmed,
			Snippet:        truncateRunes(trimmed, 120),
			Locator: DocumentSearchLocator{
				Type:        constant.DocumentReaderKindTabular,
				SheetName:   sheetName,
				RowIndex:    rowIndex,
				ColIndex:    colIndex,
				StartOffset: 0,
				EndOffset:   runeLen,
			},
		})
	}

	if ext == "csv" || ext == "tsv" {
		delimiter := ','
		if ext == "tsv" {
			delimiter = '\t'
		}
		reader := csv.NewReader(strings.NewReader(string(data)))
		reader.Comma = delimiter
		records, err := reader.ReadAll()
		if err != nil {
			return blocks
		}
		for rowIdx, row := range records {
			for colIdx, cell := range row {
				appendCell(cell, "Sheet1", rowIdx, colIdx)
				if len(blocks) >= documentIndexMaxBlocks {
					return blocks
				}
			}
		}
		return blocks
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return blocks
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		rows, rowsErr := f.GetRows(sheet)
		if rowsErr != nil {
			continue
		}
		for rowIdx, row := range rows {
			for colIdx, cell := range row {
				appendCell(cell, sheet, rowIdx, colIdx)
				if len(blocks) >= documentIndexMaxBlocks {
					return blocks
				}
			}
		}
	}

	return blocks
}

func extractPDFIndexBlocks(data []byte) []documentSearchIndexBlock {
	blocks := make([]documentSearchIndexBlock, 0)
	reader, err := rscpdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return blocks
	}

	totalPages := reader.NumPage()
	for page := 1; page <= totalPages; page++ {
		pageReader := reader.Page(page)
		if pageReader.V.IsNull() {
			continue
		}
		content := pageReader.Content()
		if len(content.Text) == 0 {
			continue
		}

		sort.Slice(content.Text, func(i, j int) bool {
			if content.Text[i].Y == content.Text[j].Y {
				return content.Text[i].X < content.Text[j].X
			}
			return content.Text[i].Y > content.Text[j].Y
		})

		var builder strings.Builder
		for _, t := range content.Text {
			builder.WriteString(t.S)
			builder.WriteByte(' ')
		}
		pageText := strings.TrimSpace(builder.String())
		if pageText == "" {
			continue
		}

		blocks = append(blocks, documentSearchIndexBlock{
			SearchableText: pageText,
			Snippet:        truncateRunes(pageText, 120),
			Locator: DocumentSearchLocator{
				Type:       constant.DocumentReaderKindPDF,
				Page:       page,
				MatchIndex: 0,
			},
		})
		if len(blocks) >= documentIndexMaxBlocks {
			return blocks
		}
	}

	return blocks
}

func truncateRunes(text string, maxLength int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	if maxLength <= 0 {
		return ""
	}
	runes := []rune(trimmed)
	if len(runes) <= maxLength {
		return trimmed
	}
	if maxLength <= 3 {
		return string(runes[:maxLength])
	}
	return string(runes[:maxLength-3]) + "..."
}

func inferFileType(fileName, mimeType string) string {
	if fileType := strings.TrimSpace(mimeToFileType[mimeType]); fileType != "" {
		return fileType
	}
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	return ext
}

func inferReaderKind(fileType, mimeType, fileName string) string {
	ft := strings.ToLower(strings.TrimSpace(fileType))
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if ft == "" {
		ft = ext
	}

	switch ft {
	case "pdf", "docx":
		return constant.DocumentReaderKindPDF
	case "xlsx", "xls", "csv", "tsv":
		return constant.DocumentReaderKindTabular
	case "txt", "md", "markdown", "log", "json", "xml", "yaml", "yml":
		return constant.DocumentReaderKindText
	default:
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mimeType)), "text/") {
			return constant.DocumentReaderKindText
		}
		return constant.DocumentReaderKindUnsupported
	}
}

func inferPreviewStatus(fileType, readerKind string) string {
	if strings.EqualFold(strings.TrimSpace(fileType), "docx") {
		return constant.DocumentPreviewProcessing
	}
	if strings.TrimSpace(readerKind) == constant.DocumentReaderKindUnsupported {
		return constant.DocumentPreviewFailed
	}
	return constant.DocumentPreviewReady
}

func effectiveReaderKind(doc model.Document) string {
	if strings.TrimSpace(doc.ReaderKind) != "" {
		return strings.TrimSpace(doc.ReaderKind)
	}
	return inferReaderKind(doc.FileType, doc.MimeType, doc.FileName)
}

func effectivePreviewStatus(doc model.Document, readerKind string) string {
	if strings.TrimSpace(doc.PreviewStatus) != "" {
		return strings.TrimSpace(doc.PreviewStatus)
	}
	return inferPreviewStatus(doc.FileType, readerKind)
}

func inferExt(fileName, fileType string) string {
	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(fileName)), ".")
	if ext != "" {
		return ext
	}
	return strings.ToLower(strings.TrimSpace(fileType))
}

func trimStringPointer(v *string) *string {
	if v == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*v)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func searchTextContent(content string, keyword string) (DocumentSearchLocator, string, bool) {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	lines := strings.Split(normalized, "\n")
	for idx, line := range lines {
		matchIdx := strings.Index(line, keyword)
		if matchIdx < 0 {
			continue
		}

		startOffset := utf8.RuneCountInString(line[:matchIdx])
		endOffset := startOffset + utf8.RuneCountInString(keyword)
		locator := DocumentSearchLocator{
			Type:        constant.DocumentReaderKindText,
			Keyword:     keyword,
			BlockIndex:  idx,
			LineStart:   idx,
			LineEnd:     idx,
			StartOffset: startOffset,
			EndOffset:   endOffset,
		}
		return locator, buildSnippet(line, matchIdx, keyword), true
	}

	return DocumentSearchLocator{}, "", false
}

func searchTabularContent(data []byte, keyword string, ext string) (DocumentSearchLocator, string, bool) {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "csv" || ext == "tsv" {
		delimiter := ','
		if ext == "tsv" {
			delimiter = '\t'
		}
		reader := csv.NewReader(strings.NewReader(string(data)))
		reader.Comma = delimiter
		records, err := reader.ReadAll()
		if err != nil {
			return DocumentSearchLocator{}, "", false
		}
		for rowIdx, row := range records {
			for colIdx, cell := range row {
				matchIdx := strings.Index(cell, keyword)
				if matchIdx < 0 {
					continue
				}
				startOffset := utf8.RuneCountInString(cell[:matchIdx])
				endOffset := startOffset + utf8.RuneCountInString(keyword)
				locator := DocumentSearchLocator{
					Type:        constant.DocumentReaderKindTabular,
					Keyword:     keyword,
					SheetName:   "Sheet1",
					RowIndex:    rowIdx,
					ColIndex:    colIdx,
					StartOffset: startOffset,
					EndOffset:   endOffset,
				}
				return locator, buildSnippet(cell, matchIdx, keyword), true
			}
		}
		return DocumentSearchLocator{}, "", false
	}

	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return DocumentSearchLocator{}, "", false
	}
	defer f.Close()

	for _, sheet := range f.GetSheetList() {
		rows, rowsErr := f.GetRows(sheet)
		if rowsErr != nil {
			continue
		}
		for rowIdx, row := range rows {
			for colIdx, cell := range row {
				matchIdx := strings.Index(cell, keyword)
				if matchIdx < 0 {
					continue
				}
				startOffset := utf8.RuneCountInString(cell[:matchIdx])
				endOffset := startOffset + utf8.RuneCountInString(keyword)
				locator := DocumentSearchLocator{
					Type:        constant.DocumentReaderKindTabular,
					Keyword:     keyword,
					SheetName:   sheet,
					RowIndex:    rowIdx,
					ColIndex:    colIdx,
					StartOffset: startOffset,
					EndOffset:   endOffset,
				}
				return locator, buildSnippet(cell, matchIdx, keyword), true
			}
		}
	}

	return DocumentSearchLocator{}, "", false
}

func searchPDFContent(data []byte, keyword string) (DocumentSearchLocator, string, bool) {
	reader, err := rscpdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return DocumentSearchLocator{}, "", false
	}

	totalPages := reader.NumPage()
	for page := 1; page <= totalPages; page++ {
		pageReader := reader.Page(page)
		if pageReader.V.IsNull() {
			continue
		}
		content := pageReader.Content()
		if len(content.Text) == 0 {
			continue
		}

		sort.Slice(content.Text, func(i, j int) bool {
			if content.Text[i].Y == content.Text[j].Y {
				return content.Text[i].X < content.Text[j].X
			}
			return content.Text[i].Y > content.Text[j].Y
		})

		var builder strings.Builder
		for _, t := range content.Text {
			builder.WriteString(t.S)
			builder.WriteByte(' ')
		}
		pageText := strings.TrimSpace(builder.String())
		if pageText == "" {
			continue
		}

		searchOffset := 0
		for {
			idx := strings.Index(pageText[searchOffset:], keyword)
			if idx < 0 {
				break
			}
			absIdx := searchOffset + idx
			locator := DocumentSearchLocator{
				Type:       constant.DocumentReaderKindPDF,
				Keyword:    keyword,
				Page:       page,
				MatchIndex: 0,
			}
			return locator, buildSnippet(pageText, absIdx, keyword), true
		}
	}

	return DocumentSearchLocator{}, "", false
}

func buildSnippet(content string, matchByteIdx int, keyword string) string {
	if matchByteIdx < 0 || matchByteIdx >= len(content) {
		trimmed := strings.TrimSpace(content)
		if trimmed == "" {
			return ""
		}
		if utf8.RuneCountInString(trimmed) > 80 {
			r := []rune(trimmed)
			return string(r[:80]) + "..."
		}
		return trimmed
	}

	startRune := utf8.RuneCountInString(content[:matchByteIdx])
	keywordRunes := utf8.RuneCountInString(keyword)
	runes := []rune(content)

	from := startRune - 24
	if from < 0 {
		from = 0
	}
	to := startRune + keywordRunes + 24
	if to > len(runes) {
		to = len(runes)
	}

	snippet := strings.TrimSpace(string(runes[from:to]))
	if from > 0 {
		snippet = "..." + snippet
	}
	if to < len(runes) {
		snippet += "..."
	}
	return snippet
}
