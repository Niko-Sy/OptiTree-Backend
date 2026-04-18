package service

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/config"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

var (
	ErrAITaskNoDocuments             = errors.New("没有可用文档")
	ErrAITaskCallbackTaskNotFound    = errors.New("任务不存在")
	ErrAITaskCallbackInvalidStatus   = errors.New("任务状态非法")
	ErrAITaskCallbackProjectMismatch = errors.New("回调 projectId 与任务不一致")
)

const (
	aiTaskStreamSourceWorker   = "worker"
	aiTaskStreamSourceProducer = "producer"
)

type AITaskService struct {
	taskRepo           *repository.AITaskRepository
	docRepo            *repository.DocumentRepository
	docConvertTaskRepo *repository.DocumentConversionTaskRepository
	projectRepo        *repository.ProjectRepository
	memberRepo         *repository.MemberRepository
	projectService     *ProjectService
	ftService          *FaultTreeService
	kgService          *KnowledgeGraphService
	progressHub        *TaskProgressHub
	rdb                *redis.Client

	queueStream    string
	queueMaxLen    int64
	callbackHeader string
	callbackToken  string

	producerStream      string
	producerGroup       string
	producerReadCount   int64
	producerBlockMS     int64
	producerDelayedZSet string
	producerRetryDelay  int64
	dispatcherWorkers   int
	projectLockTTL      time.Duration
	callbackDedupeTTL   time.Duration
	snapshotTTL         time.Duration

	ftNodeDefaults config.FaultTreeNodeDefaultsConfig

	dispatcherMu      sync.Mutex
	dispatcherCtx     context.Context
	dispatcherCancel  context.CancelFunc
	dispatcherWG      sync.WaitGroup
	dispatcherStarted bool
}

type AITaskQueueDocument struct {
	ID        string `json:"id"`
	FileName  string `json:"fileName"`
	FileType  string `json:"fileType"`
	SourceURL string `json:"sourceUrl"`
}

type AITaskQueueMessage struct {
	TaskID    string                `json:"taskId"`
	ProjectID string                `json:"projectId"`
	TaskType  string                `json:"taskType"`
	UserID    string                `json:"userId"`
	TopEvent  string                `json:"topEvent,omitempty"`
	DocIDs    []string              `json:"docIds"`
	Documents []AITaskQueueDocument `json:"documents"`
	Config    ai.GenerateConfig     `json:"config"`
	Attempt   int                   `json:"attempt"`
	TraceID   string                `json:"traceId,omitempty"`
	CreatedAt string                `json:"createdAt"`
}

type TaskCallbackInput struct {
	TaskID       string                 `json:"taskId"`
	ProjectID    string                 `json:"projectId,omitempty"`
	TraceID      string                 `json:"traceId,omitempty"`
	EventKey     string                 `json:"eventKey,omitempty"`
	EventVersion int                    `json:"eventVersion,omitempty"`
	Status       string                 `json:"status"`
	Progress     int                    `json:"progress"`
	Stage        string                 `json:"stage,omitempty"`
	StageLabel   string                 `json:"stageLabel,omitempty"`
	ErrorMessage string                 `json:"errorMessage,omitempty"`
	Result       map[string]interface{} `json:"result,omitempty"`
	WorkerID     string                 `json:"workerId,omitempty"`
	Attempt      int                    `json:"attempt,omitempty"`
}

func NewAITaskService(
	taskRepo *repository.AITaskRepository,
	docRepo *repository.DocumentRepository,
	docConvertTaskRepo *repository.DocumentConversionTaskRepository,
	projectRepo *repository.ProjectRepository,
	memberRepo *repository.MemberRepository,
	projectService *ProjectService,
	ftService *FaultTreeService,
	kgService *KnowledgeGraphService,
	progressHub *TaskProgressHub,
	rdb *redis.Client,
	queueStream string,
	queueMaxLen int64,
	callbackHeader string,
	callbackToken string,
	producerStream string,
	producerGroup string,
	producerReadCount int64,
	producerBlockMS int64,
	dispatcherWorkers int,
	producerDelayedZSet string,
	producerRetryDelay int64,
	projectLockTTL time.Duration,
	callbackDedupeTTL time.Duration,
	snapshotTTL time.Duration,
	nodeDefaults config.FaultTreeNodeDefaultsConfig,
) *AITaskService {
	if strings.TrimSpace(queueStream) == "" {
		queueStream = "stream:ai:tasks"
	}
	if queueMaxLen <= 0 {
		queueMaxLen = 10000
	}
	if strings.TrimSpace(callbackHeader) == "" {
		callbackHeader = "X-Internal-Token"
	}
	if strings.TrimSpace(producerStream) == "" {
		producerStream = queueStream + ":producer"
	}
	if strings.TrimSpace(producerGroup) == "" {
		producerGroup = "ai-ft-producer"
	}
	if producerReadCount <= 0 {
		producerReadCount = 10
	}
	if producerBlockMS <= 0 {
		producerBlockMS = 3000
	}
	if dispatcherWorkers <= 0 {
		dispatcherWorkers = 5
	}
	if strings.TrimSpace(producerDelayedZSet) == "" {
		producerDelayedZSet = "zset:ai:tasks:producer:delayed"
	}
	if producerRetryDelay <= 0 {
		producerRetryDelay = 2000
	}
	if projectLockTTL <= 0 {
		projectLockTTL = 20 * time.Minute
	}
	if callbackDedupeTTL <= 0 {
		callbackDedupeTTL = 24 * time.Hour
	}
	if snapshotTTL <= 0 {
		snapshotTTL = 24 * time.Hour
	}
	return &AITaskService{
		taskRepo:            taskRepo,
		docRepo:             docRepo,
		docConvertTaskRepo:  docConvertTaskRepo,
		projectRepo:         projectRepo,
		memberRepo:          memberRepo,
		projectService:      projectService,
		ftService:           ftService,
		kgService:           kgService,
		progressHub:         progressHub,
		rdb:                 rdb,
		queueStream:         strings.TrimSpace(queueStream),
		queueMaxLen:         queueMaxLen,
		callbackHeader:      strings.TrimSpace(callbackHeader),
		callbackToken:       strings.TrimSpace(callbackToken),
		producerStream:      strings.TrimSpace(producerStream),
		producerGroup:       strings.TrimSpace(producerGroup),
		producerReadCount:   producerReadCount,
		producerBlockMS:     producerBlockMS,
		producerDelayedZSet: strings.TrimSpace(producerDelayedZSet),
		producerRetryDelay:  producerRetryDelay,
		dispatcherWorkers:   dispatcherWorkers,
		projectLockTTL:      projectLockTTL,
		callbackDedupeTTL:   callbackDedupeTTL,
		snapshotTTL:         snapshotTTL,
		ftNodeDefaults:      nodeDefaults,
	}
}

func (s *AITaskService) CallbackHeader() string {
	return s.callbackHeader
}

func (s *AITaskService) AuthorizeCallback(token string) bool {
	if strings.TrimSpace(s.callbackToken) == "" {
		return false
	}
	provided := strings.TrimSpace(token)
	expected := strings.TrimSpace(s.callbackToken)
	return subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

// GetTask returns the current state of an AI task.
func (s *AITaskService) GetTask(ctx context.Context, taskID string) (*model.AITask, error) {
	return s.taskRepo.FindByID(taskID)
}

func (s *AITaskService) GetLatestFaultTreeTaskByProject(ctx context.Context, projectID, userID string) (*model.AITask, error) {
	projectID = strings.TrimSpace(projectID)
	userID = strings.TrimSpace(userID)
	if projectID == "" {
		return nil, ErrProjectNotFound
	}

	project, err := s.projectRepo.FindByID(projectID)
	if err != nil {
		return nil, err
	}
	if project == nil {
		return nil, ErrProjectNotFound
	}
	if project.Type != constant.ProjectTypeFT {
		return nil, ErrProjectTypeMismatch
	}

	member, err := s.memberRepo.FindByProjectAndUser(projectID, userID)
	if err != nil {
		return nil, err
	}
	if member == nil {
		return nil, ErrProjectPermissionDenied
	}

	if s.rdb != nil {
		if snapshotText, snapshotErr := s.rdb.Get(ctx, constant.RedisKeyAITaskLatest+projectID).Result(); snapshotErr == nil && strings.TrimSpace(snapshotText) != "" {
			var snapshot TaskProgressEvent
			if jsonErr := json.Unmarshal([]byte(snapshotText), &snapshot); jsonErr == nil {
				snapshotTaskID := strings.TrimSpace(snapshot.TaskID)
				if snapshotTaskID != "" {
					task, findErr := s.taskRepo.FindByID(snapshotTaskID)
					if findErr != nil {
						return nil, findErr
					}
					if task != nil && task.ProjectID != nil && strings.TrimSpace(*task.ProjectID) == projectID && task.Type == constant.AITaskTypeGenerateFaultTree {
						return task, nil
					}
				}
			}
		}
	}

	return s.taskRepo.FindLatestByProjectAndType(projectID, constant.AITaskTypeGenerateFaultTree)
}

func (s *AITaskService) cacheStatus(ctx context.Context, id, status string, progress int, stage, stageLabel string) {
	key := constant.RedisKeyAITask + id
	_ = s.rdb.HSet(ctx, key,
		"status", status,
		"progress", progress,
		"stage", stage,
		"stageLabel", stageLabel,
	).Err()
	_ = s.rdb.Expire(ctx, key, 24*time.Hour).Err()
}

func (s *AITaskService) createTask(
	ctx context.Context,
	taskType, modelName, userID string,
	projectID *string,
	idempotencyKey string,
) (*model.AITask, bool, error) {
	now := time.Now().UTC()
	task := &model.AITask{
		ID:           util.NewAITaskID(),
		Type:         taskType,
		Status:       constant.AITaskStatusPending,
		Progress:     0,
		Stage:        "queued",
		StageLabel:   "任务已入队，等待 Worker 处理",
		Model:        modelName,
		CreatedBy:    userID,
		ProjectID:    projectID,
		AttemptCount: 0,
		MaxAttempts:  3,
		QueuedAt:     &now,
	}
	if strings.TrimSpace(idempotencyKey) != "" {
		v := strings.TrimSpace(idempotencyKey)
		task.IdempotencyKey = &v
	}
	if err := s.taskRepo.Create(task); err != nil {
		if task.IdempotencyKey != nil && isAITaskIdempotencyConflict(err) {
			existing, findErr := s.taskRepo.FindByIdempotencyKey(*task.IdempotencyKey)
			if findErr != nil {
				return nil, false, findErr
			}
			if existing != nil {
				s.cacheStatus(ctx, existing.ID, existing.Status, existing.Progress, existing.Stage, existing.StageLabel)
				return existing, false, nil
			}
		}
		return nil, false, err
	}
	s.cacheStatus(ctx, task.ID, task.Status, task.Progress, task.Stage, task.StageLabel)
	return task, true, nil
}

func isAITaskIdempotencyConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "uq_ai_tasks_idempotency_key") {
		return true
	}
	return strings.Contains(msg, "duplicate key value") && strings.Contains(msg, "idempotency_key")
}

func projectGenerationStatusFromTaskStatus(taskStatus string) string {
	switch strings.TrimSpace(taskStatus) {
	case constant.AITaskStatusCompleted:
		return constant.ProjectGenerationCompleted
	case constant.AITaskStatusFailed, constant.AITaskStatusDead:
		return constant.ProjectGenerationFailed
	case constant.AITaskStatusProcessing, constant.AITaskStatusRetrying:
		return constant.ProjectGenerationRunning
	default:
		return constant.ProjectGenerationPending
	}
}

func (s *AITaskService) collectTaskDocuments(docIDs []string) ([]AITaskQueueDocument, error) {
	if len(docIDs) == 0 {
		return nil, ErrAITaskNoDocuments
	}
	docs, err := s.docRepo.FindByIDsOrdered(docIDs)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, ErrAITaskNoDocuments
	}

	byID := make(map[string]model.Document, len(docs))
	for _, doc := range docs {
		byID[doc.ID] = doc
	}

	refs := make([]AITaskQueueDocument, 0, len(docIDs))
	for _, id := range docIDs {
		doc, ok := byID[id]
		if !ok {
			continue
		}
		refs = append(refs, AITaskQueueDocument{
			ID:        doc.ID,
			FileName:  doc.FileName,
			FileType:  doc.FileType,
			SourceURL: doc.SourceURL,
		})
	}
	if len(refs) == 0 {
		return nil, ErrAITaskNoDocuments
	}
	return refs, nil
}

func normalizeDocIDs(docIDs []string) []string {
	if len(docIDs) == 0 {
		return []string{}
	}

	normalized := make([]string, 0, len(docIDs))
	seen := make(map[string]struct{}, len(docIDs))
	for _, id := range docIDs {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized
}

func projectIDValue(projectID *string) string {
	if projectID == nil {
		return ""
	}
	return strings.TrimSpace(*projectID)
}

func (s *AITaskService) normalizeTaskDocumentsToProject(
	ctx context.Context,
	targetProjectID string,
	userID string,
	docIDs []string,
) ([]string, error) {
	targetProjectID = strings.TrimSpace(targetProjectID)
	userID = strings.TrimSpace(userID)
	normalizedIDs := normalizeDocIDs(docIDs)
	if targetProjectID == "" || len(normalizedIDs) == 0 {
		return normalizedIDs, nil
	}

	docs, err := s.docRepo.FindByIDsOrdered(normalizedIDs)
	if err != nil {
		return nil, err
	}
	if len(docs) == 0 {
		return nil, ErrAITaskNoDocuments
	}

	// Make sure orphan docs are persisted to the target project before task enqueue.
	orphanIDs := make([]string, 0)
	for i := range docs {
		doc := docs[i]
		docProjectID := projectIDValue(doc.ProjectID)
		if docProjectID == "" {
			if strings.TrimSpace(doc.UploadedBy) != userID {
				return nil, ErrProjectPermissionDenied
			}
			orphanIDs = append(orphanIDs, doc.ID)
			continue
		}
		if docProjectID == targetProjectID {
			continue
		}

		member, memberErr := s.memberRepo.FindByProjectAndUser(docProjectID, userID)
		if memberErr != nil {
			return nil, memberErr
		}
		if member == nil {
			return nil, ErrProjectPermissionDenied
		}
	}
	if len(orphanIDs) > 0 {
		if err := s.docRepo.BindOrphanDocsToProject(orphanIDs, targetProjectID); err != nil {
			return nil, err
		}
		docs, err = s.docRepo.FindByIDsOrdered(normalizedIDs)
		if err != nil {
			return nil, err
		}
	}

	effective := make([]string, 0, len(normalizedIDs))
	clonedCount := 0
	for i := range docs {
		doc := docs[i]
		docProjectID := projectIDValue(doc.ProjectID)
		switch {
		case docProjectID == "":
			// Should not happen after BindOrphanDocsToProject, but keep safe fallback.
			effective = append(effective, doc.ID)
		case docProjectID == targetProjectID:
			effective = append(effective, doc.ID)
		default:
			cloned, cloneErr := s.docRepo.CloneToProject(doc, targetProjectID, userID)
			if cloneErr != nil {
				return nil, cloneErr
			}
			if strings.EqualFold(strings.TrimSpace(cloned.FileType), "docx") && cloned.DerivedPdfDocID == nil && s.docConvertTaskRepo != nil {
				if upsertErr := s.docConvertTaskRepo.UpsertPendingByDocument(cloned.ID, cloned.ProjectID); upsertErr != nil {
					errMsg := "DOCX 异步转换任务创建失败"
					_ = s.docRepo.UpdatePreviewMeta(cloned.ID, constant.DocumentPreviewFailed, nil, &errMsg)
					_ = s.docConvertTaskRepo.SetFailedByDocument(cloned.ID, errMsg)
					log.Warn().Err(upsertErr).Str("docId", cloned.ID).Str("projectId", targetProjectID).Msg("upsert cloned DOCX conversion task failed")
				} else if s.rdb != nil {
					if queueErr := enqueueDocumentConversionTask(ctx, s.rdb, cloned.ID, "ai_generate_clone"); queueErr != nil {
						errMsg := "DOCX 异步转换任务入队失败"
						_ = s.docRepo.UpdatePreviewMeta(cloned.ID, constant.DocumentPreviewFailed, nil, &errMsg)
						_ = s.docConvertTaskRepo.SetFailedByDocument(cloned.ID, errMsg)
						log.Warn().Err(queueErr).Str("docId", cloned.ID).Str("projectId", targetProjectID).Msg("enqueue cloned DOCX conversion task failed")
					}
				}
			}
			clonedCount++
			effective = append(effective, cloned.ID)
		}
	}

	if len(effective) == 0 {
		return nil, ErrAITaskNoDocuments
	}

	log.Info().
		Str("projectId", targetProjectID).
		Str("userId", userID).
		Int("inputDocCount", len(normalizedIDs)).
		Int("boundOrphanCount", len(orphanIDs)).
		Int("clonedDocCount", clonedCount).
		Int("effectiveDocCount", len(effective)).
		Msg("normalized AI generation documents")

	return effective, nil
}

func buildIdempotencyKey(
	taskType, userID string,
	projectID *string,
	topEvent string,
	docIDs []string,
	cfg ai.GenerateConfig,
) string {
	sortedDocIDs := make([]string, len(docIDs))
	copy(sortedDocIDs, docIDs)
	sort.Strings(sortedDocIDs)

	payload := map[string]interface{}{
		"taskType": taskType,
		"userID":   strings.TrimSpace(userID),
		"projectID": strings.TrimSpace(func() string {
			if projectID == nil {
				return ""
			}
			return *projectID
		}()),
		"topEvent": strings.TrimSpace(topEvent),
		"docIDs":   sortedDocIDs,
		"config": map[string]interface{}{
			"quality":     cfg.Quality,
			"model":       cfg.Model,
			"depth":       cfg.Depth,
			"maxNodes":    cfg.MaxNodes,
			"entityTypes": cfg.EntityTypes,
		},
	}
	b, _ := json.Marshal(payload)
	return util.SHA256(string(b))
}

func defaultFaultTreeGenerateConfig() ai.GenerateConfig {
	return ai.GenerateConfig{
		Quality:  "balanced",
		Depth:    4,
		MaxNodes: 30,
	}
}

func defaultKnowledgeGraphGenerateConfig() ai.GenerateConfig {
	return ai.GenerateConfig{
		Quality:     "balanced",
		EntityTypes: []string{},
	}
}

func (s *AITaskService) enqueueTask(ctx context.Context, msg AITaskQueueMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化任务消息失败: %w", err)
	}

	entryID, err := s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: s.queueStream,
		MaxLen: s.queueMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"taskId":    msg.TaskID,
			"projectId": msg.ProjectID,
			"taskType":  msg.TaskType,
			"attempt":   msg.Attempt,
			"payload":   string(payload),
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("写入 Redis Stream 失败: %w", err)
	}
	s.recordTaskStreamEntry(ctx, msg.TaskID, aiTaskStreamSourceWorker, entryID)
	return nil
}

func (s *AITaskService) enqueueFaultTreeProducerTask(ctx context.Context, msg AITaskQueueMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化生产任务失败: %w", err)
	}

	entryID, err := s.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: s.producerStream,
		MaxLen: s.queueMaxLen,
		Approx: true,
		Values: map[string]interface{}{
			"taskId":    msg.TaskID,
			"projectId": msg.ProjectID,
			"taskType":  msg.TaskType,
			"payload":   string(payload),
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("写入生产接入流失败: %w", err)
	}
	s.recordTaskStreamEntry(ctx, msg.TaskID, aiTaskStreamSourceProducer, entryID)
	return nil
}

func (s *AITaskService) taskStreamEntryKey(taskID, source string) string {
	tid := strings.TrimSpace(taskID)
	if tid == "" {
		return ""
	}

	switch strings.TrimSpace(source) {
	case aiTaskStreamSourceWorker:
		return constant.RedisKeyAITaskWorkerStreamEntries + tid
	case aiTaskStreamSourceProducer:
		return constant.RedisKeyAITaskProducerStreamEntries + tid
	default:
		return ""
	}
}

func (s *AITaskService) recordTaskStreamEntry(ctx context.Context, taskID, source, entryID string) {
	if s.rdb == nil {
		return
	}
	tid := strings.TrimSpace(taskID)
	entry := strings.TrimSpace(entryID)
	if tid == "" || entry == "" {
		return
	}

	key := s.taskStreamEntryKey(tid, source)
	if key == "" {
		return
	}

	if err := s.rdb.SAdd(ctx, key, entry).Err(); err != nil {
		log.Warn().Err(err).Str("taskId", tid).Str("streamSource", source).Str("entryId", entry).Msg("记录 stream entry 失败")
		return
	}
	if err := s.rdb.Expire(ctx, key, s.snapshotTTL).Err(); err != nil {
		log.Warn().Err(err).Str("taskId", tid).Str("streamSource", source).Msg("设置 stream entry 跟踪 TTL 失败")
	}
}

func (s *AITaskService) clearTaskStreamEntries(ctx context.Context, taskID string) {
	if s.rdb == nil {
		return
	}
	tid := strings.TrimSpace(taskID)
	if tid == "" {
		return
	}

	targets := []struct {
		key    string
		stream string
		source string
	}{
		{key: s.taskStreamEntryKey(tid, aiTaskStreamSourceWorker), stream: s.queueStream, source: aiTaskStreamSourceWorker},
		{key: s.taskStreamEntryKey(tid, aiTaskStreamSourceProducer), stream: s.producerStream, source: aiTaskStreamSourceProducer},
	}

	for _, target := range targets {
		if target.key == "" || strings.TrimSpace(target.stream) == "" {
			continue
		}

		entryIDs, err := s.rdb.SMembers(ctx, target.key).Result()
		if err != nil && err != redis.Nil {
			log.Warn().Err(err).Str("taskId", tid).Str("streamSource", target.source).Msg("读取 stream entry 跟踪失败")
			continue
		}

		if len(entryIDs) > 0 {
			if err := s.rdb.XDel(ctx, target.stream, entryIDs...).Err(); err != nil {
				log.Warn().Err(err).
					Str("taskId", tid).
					Str("stream", target.stream).
					Str("streamSource", target.source).
					Int("entryCount", len(entryIDs)).
					Msg("清理 stream entry 失败")
				continue
			}
		}

		if err := s.rdb.Del(ctx, target.key).Err(); err != nil {
			log.Warn().Err(err).Str("taskId", tid).Str("streamSource", target.source).Msg("删除 stream entry 跟踪 key 失败")
		}
	}
}

// Generate Fault Tree

type GenerateFaultTreeInput struct {
	DocIDs    []string
	TopEvent  string
	ProjectID *string
	UserID    string
}

type GenerateFaultTreeOutput struct {
	TaskID          string   `json:"taskId"`
	Status          string   `json:"status"`
	ProjectID       string   `json:"projectId"`
	EffectiveDocIDs []string `json:"effectiveDocIds,omitempty"`
}

func (s *AITaskService) GenerateFaultTree(ctx context.Context, input GenerateFaultTreeInput) (*GenerateFaultTreeOutput, error) {
	cfg := defaultFaultTreeGenerateConfig()
	project, err := s.resolveOrCreateProject(ctx, input.ProjectID, input.UserID, constant.ProjectTypeFT, input.TopEvent)
	if err != nil {
		return nil, err
	}
	if err := s.setProjectGenerationStatus(project.ID, constant.ProjectGenerationPending); err != nil {
		return nil, err
	}

	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = "default"
	}
	projectID := project.ID
	requestDocIDs := normalizeDocIDs(input.DocIDs)
	idem := buildIdempotencyKey(constant.AITaskTypeGenerateFaultTree, input.UserID, &projectID, input.TopEvent, requestDocIDs, cfg)
	task, created, err := s.createTask(ctx, constant.AITaskTypeGenerateFaultTree, modelName, input.UserID, &projectID, idem)
	if err != nil {
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		return nil, err
	}
	if !created {
		_ = s.setProjectGenerationStatus(projectID, projectGenerationStatusFromTaskStatus(task.Status))
		return &GenerateFaultTreeOutput{TaskID: task.ID, Status: task.Status, ProjectID: projectID}, nil
	}

	effectiveDocIDs, err := s.normalizeTaskDocumentsToProject(ctx, projectID, input.UserID, requestDocIDs)
	if err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	documents, err := s.collectTaskDocuments(effectiveDocIDs)
	if err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	message := AITaskQueueMessage{
		TaskID:    task.ID,
		ProjectID: projectID,
		TaskType:  constant.AITaskTypeGenerateFaultTree,
		UserID:    input.UserID,
		TopEvent:  input.TopEvent,
		DocIDs:    effectiveDocIDs,
		Documents: documents,
		Config:    cfg,
		Attempt:   0,
		TraceID:   task.ID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.enqueueFaultTreeProducerTask(ctx, message); err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	_ = s.taskRepo.UpdateStatus(task.ID, constant.AITaskStatusPending, 1, "producer_queued", "任务已进入调度队列")
	s.cacheStatus(ctx, task.ID, constant.AITaskStatusPending, 1, "producer_queued", "任务已进入调度队列")

	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        task.ID,
		Status:        constant.AITaskStatusPending,
		ProjectStatus: constant.ProjectGenerationPending,
		Progress:      1,
		Stage:         "producer_queued",
		StageLabel:    "任务已进入调度队列",
	})

	return &GenerateFaultTreeOutput{
		TaskID:          task.ID,
		Status:          task.Status,
		ProjectID:       projectID,
		EffectiveDocIDs: effectiveDocIDs,
	}, nil
}

// Generate Knowledge Graph

type GenerateKnowledgeGraphInput struct {
	DocIDs    []string
	ProjectID *string
	UserID    string
}

type GenerateKnowledgeGraphOutput struct {
	TaskID          string   `json:"taskId"`
	Status          string   `json:"status"`
	ProjectID       string   `json:"projectId"`
	EffectiveDocIDs []string `json:"effectiveDocIds,omitempty"`
}

func (s *AITaskService) GenerateKnowledgeGraph(ctx context.Context, input GenerateKnowledgeGraphInput) (*GenerateKnowledgeGraphOutput, error) {
	cfg := defaultKnowledgeGraphGenerateConfig()
	project, err := s.resolveOrCreateProject(ctx, input.ProjectID, input.UserID, constant.ProjectTypeKG, "")
	if err != nil {
		return nil, err
	}
	if err := s.setProjectGenerationStatus(project.ID, constant.ProjectGenerationPending); err != nil {
		return nil, err
	}

	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = "default"
	}
	projectID := project.ID
	requestDocIDs := normalizeDocIDs(input.DocIDs)
	idem := buildIdempotencyKey(constant.AITaskTypeGenerateKnowledgeGraph, input.UserID, &projectID, "", requestDocIDs, cfg)
	task, created, err := s.createTask(ctx, constant.AITaskTypeGenerateKnowledgeGraph, modelName, input.UserID, &projectID, idem)
	if err != nil {
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		return nil, err
	}
	if !created {
		_ = s.setProjectGenerationStatus(projectID, projectGenerationStatusFromTaskStatus(task.Status))
		return &GenerateKnowledgeGraphOutput{TaskID: task.ID, Status: task.Status, ProjectID: projectID}, nil
	}

	effectiveDocIDs, err := s.normalizeTaskDocumentsToProject(ctx, projectID, input.UserID, requestDocIDs)
	if err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	documents, err := s.collectTaskDocuments(effectiveDocIDs)
	if err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	message := AITaskQueueMessage{
		TaskID:    task.ID,
		ProjectID: projectID,
		TaskType:  constant.AITaskTypeGenerateKnowledgeGraph,
		UserID:    input.UserID,
		DocIDs:    effectiveDocIDs,
		Documents: documents,
		Config:    cfg,
		Attempt:   0,
		TraceID:   task.ID,
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := s.enqueueTask(ctx, message); err != nil {
		_ = s.taskRepo.SetFailed(task.ID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, task.ID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return nil, err
	}

	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.pending",
		ProjectID:     projectID,
		TaskID:        task.ID,
		Status:        task.Status,
		ProjectStatus: constant.ProjectGenerationPending,
		Progress:      0,
		Stage:         task.Stage,
		StageLabel:    task.StageLabel,
	})

	return &GenerateKnowledgeGraphOutput{
		TaskID:          task.ID,
		Status:          task.Status,
		ProjectID:       projectID,
		EffectiveDocIDs: effectiveDocIDs,
	}, nil
}

func (s *AITaskService) HandleTaskCallback(ctx context.Context, input TaskCallbackInput) error {
	taskID := strings.TrimSpace(input.TaskID)
	if taskID == "" {
		return ErrAITaskCallbackTaskNotFound
	}

	eventKey := strings.TrimSpace(input.EventKey)
	if eventKey != "" {
		dedupeKey := constant.RedisKeyAITaskDedupe + taskID + ":" + eventKey
		ok, err := s.rdb.SetNX(ctx, dedupeKey, "1", s.callbackDedupeTTL).Result()
		if err != nil {
			log.Warn().Err(err).Str("taskId", taskID).Msg("callback 去重写入失败，继续处理")
		} else if !ok {
			return nil
		}
	}

	task, err := s.taskRepo.FindByID(taskID)
	if err != nil {
		return err
	}
	if task == nil {
		return ErrAITaskCallbackTaskNotFound
	}
	if task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		return ErrProjectNotFound
	}

	projectID := strings.TrimSpace(*task.ProjectID)
	if inPID := strings.TrimSpace(input.ProjectID); inPID != "" && inPID != projectID {
		return ErrAITaskCallbackProjectMismatch
	}

	status := strings.TrimSpace(input.Status)
	if status == "" {
		return ErrAITaskCallbackInvalidStatus
	}

	if isAITaskTerminalStatus(task.Status) {
		if task.Status == constant.AITaskStatusCompleted && status == constant.AITaskStatusCompleted {
			s.releaseProjectLock(ctx, projectID, taskID)
			s.clearTaskStreamEntries(ctx, taskID)
			return nil
		}
		if !(task.Status == constant.AITaskStatusFailed && status == constant.AITaskStatusDead) {
			return nil
		}
	}

	stage := strings.TrimSpace(input.Stage)
	stageLabel := strings.TrimSpace(input.StageLabel)
	progress := input.Progress
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	attempt := input.Attempt
	if attempt < 0 {
		attempt = 0
	}
	if attempt == 0 {
		attempt = task.AttemptCount
	}
	if attempt > 0 && task.AttemptCount > 0 && attempt < task.AttemptCount {
		return nil
	}
	if !isAITaskTerminalStatus(status) && progress < task.Progress && attempt <= task.AttemptCount {
		return nil
	}

	workerID := strings.TrimSpace(input.WorkerID)
	var workerPtr *string
	if workerID != "" {
		workerPtr = &workerID
	}

	switch status {
	case constant.AITaskStatusProcessing, constant.AITaskStatusRetrying:
		s.touchProjectLock(ctx, projectID, taskID)
		if stage == "" {
			stage = "processing"
		}
		if stageLabel == "" {
			stageLabel = "任务处理中"
		}
		if err := s.taskRepo.UpdateStatusExt(taskID, status, progress, stage, stageLabel, workerPtr, attempt, nil); err != nil {
			return err
		}
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationRunning)
		s.cacheStatus(ctx, taskID, status, progress, stage, stageLabel)
		eventName := "task.progress"
		if status == constant.AITaskStatusRetrying {
			eventName = "task.retrying"
		}
		s.publishTaskEvent(TaskProgressEvent{
			Event:         eventName,
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        status,
			ProjectStatus: constant.ProjectGenerationRunning,
			Progress:      progress,
			Stage:         stage,
			StageLabel:    stageLabel,
		})
		return nil

	case constant.AITaskStatusCompleted:
		if stage == "" {
			stage = "completed"
		}
		if stageLabel == "" {
			stageLabel = "生成完成"
		}
		normalizedResult := normalizeTaskResultPayload(task.Type, input.Result)
		if err := s.persistGeneratedGraph(ctx, task, normalizedResult); err != nil {
			errMsg := "保存生成结果失败: " + err.Error()
			_ = s.taskRepo.SetFailed(taskID, errMsg)
			s.releaseProjectLock(ctx, projectID, taskID)
			s.clearTaskStreamEntries(ctx, taskID)
			_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
			s.cacheStatus(ctx, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
			s.publishTaskEvent(TaskProgressEvent{
				Event:         "task.failed",
				ProjectID:     projectID,
				TaskID:        taskID,
				Status:        constant.AITaskStatusFailed,
				ProjectStatus: constant.ProjectGenerationFailed,
				ErrorMessage:  errMsg,
				Stage:         "failed",
				StageLabel:    "生成失败",
			})
			return err
		}

		resultJSON, _ := json.Marshal(normalizedResult)
		if err := s.taskRepo.SetCompleted(taskID, resultJSON); err != nil {
			s.releaseProjectLock(ctx, projectID, taskID)
			s.clearTaskStreamEntries(ctx, taskID)
			return err
		}
		s.releaseProjectLock(ctx, projectID, taskID)
		s.clearTaskStreamEntries(ctx, taskID)
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationCompleted)
		if task.Type == constant.AITaskTypeGenerateFaultTree && s.projectService != nil {
			s.projectService.invalidateProjectListCachesByProject(ctx, projectID)
		}
		s.cacheStatus(ctx, taskID, constant.AITaskStatusCompleted, 100, stage, stageLabel)
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.completed",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusCompleted,
			ProjectStatus: constant.ProjectGenerationCompleted,
			Progress:      100,
			Stage:         stage,
			StageLabel:    stageLabel,
			Result:        extractResultSummary(task.Type, normalizedResult),
		})
		return nil

	case constant.AITaskStatusFailed, constant.AITaskStatusDead:
		if stage == "" {
			stage = "failed"
		}
		if stageLabel == "" {
			stageLabel = "生成失败"
		}
		errMsg := strings.TrimSpace(input.ErrorMessage)
		if errMsg == "" {
			errMsg = "任务执行失败"
		}
		if err := s.taskRepo.UpdateStatusExt(taskID, status, progress, stage, stageLabel, workerPtr, attempt, &errMsg); err != nil {
			return err
		}
		s.releaseProjectLock(ctx, projectID, taskID)
		s.clearTaskStreamEntries(ctx, taskID)
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(ctx, taskID, status, progress, stage, stageLabel)
		eventName := "task.failed"
		if status == constant.AITaskStatusDead {
			eventName = "task.dead"
		}
		s.publishTaskEvent(TaskProgressEvent{
			Event:         eventName,
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        status,
			ProjectStatus: constant.ProjectGenerationFailed,
			ErrorMessage:  errMsg,
			Progress:      progress,
			Stage:         stage,
			StageLabel:    stageLabel,
		})
		return nil
	default:
		return ErrAITaskCallbackInvalidStatus
	}
}

func isAITaskTerminalStatus(status string) bool {
	switch status {
	case constant.AITaskStatusCompleted, constant.AITaskStatusFailed, constant.AITaskStatusDead:
		return true
	default:
		return false
	}
}

func (s *AITaskService) persistGeneratedGraph(ctx context.Context, task *model.AITask, raw map[string]interface{}) error {
	if task.ProjectID == nil || strings.TrimSpace(*task.ProjectID) == "" {
		return ErrProjectNotFound
	}
	projectID := strings.TrimSpace(*task.ProjectID)

	switch task.Type {
	case constant.AITaskTypeGenerateFaultTree:
		result, err := parseFaultTreeResult(raw)
		if err != nil {
			return err
		}
		return s.saveGeneratedFaultTreeToProject(ctx, projectID, result)
	case constant.AITaskTypeGenerateKnowledgeGraph:
		result, err := parseKnowledgeGraphResult(raw)
		if err != nil {
			return err
		}
		return s.saveGeneratedKnowledgeGraphToProject(ctx, projectID, result)
	default:
		return fmt.Errorf("不支持的任务类型: %s", task.Type)
	}
}

func parseFaultTreeResult(raw map[string]interface{}) (*ai.FaultTreeResult, error) {
	normalized := normalizeFaultTreeResultPayload(raw)
	b, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	var result ai.FaultTreeResult
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	if len(result.Nodes) == 0 {
		return nil, ErrAIGenerationResultFormat
	}
	return &result, nil
}

func parseKnowledgeGraphResult(raw map[string]interface{}) (*ai.KnowledgeGraphResult, error) {
	normalized := normalizeKnowledgeGraphResultPayload(raw)
	b, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	var result ai.KnowledgeGraphResult
	if err := json.Unmarshal(b, &result); err != nil {
		return nil, err
	}
	if len(result.Nodes) == 0 {
		return nil, ErrAIGenerationResultFormat
	}
	if result.EntityCount == 0 {
		result.EntityCount = len(result.Nodes)
	}
	if result.RelationCount == 0 {
		result.RelationCount = len(result.Edges)
	}
	return &result, nil
}

func normalizeTaskResultPayload(taskType string, raw map[string]interface{}) map[string]interface{} {
	switch strings.TrimSpace(taskType) {
	case constant.AITaskTypeGenerateFaultTree:
		return normalizeFaultTreeResultPayload(raw)
	case constant.AITaskTypeGenerateKnowledgeGraph:
		return normalizeKnowledgeGraphResultPayload(raw)
	default:
		return cloneAnyMap(raw)
	}
}

func normalizeFaultTreeResultPayload(raw map[string]interface{}) map[string]interface{} {
	normalized := cloneAnyMap(raw)

	faultTree := toAnyMap(raw["faultTree"])
	if _, ok := normalized["nodes"]; !ok {
		if nodes, exists := faultTree["nodes"]; exists {
			normalized["nodes"] = nodes
		}
	}
	if _, ok := normalized["edges"]; !ok {
		if edges, exists := faultTree["edges"]; exists {
			normalized["edges"] = edges
		}
	}

	if _, ok := normalized["accuracy"]; !ok {
		if accuracy, exists := faultTree["accuracy"]; exists {
			normalized["accuracy"] = accuracy
		}
	}
	if _, ok := normalized["accuracy"]; !ok {
		summaryMap := toAnyMap(raw["summary"])
		qualityMetrics := toAnyMap(summaryMap["qualityMetrics"])
		if accuracy, exists := qualityMetrics["accuracy"]; exists {
			normalized["accuracy"] = accuracy
		} else if score, exists := qualityMetrics["score"]; exists {
			normalized["accuracy"] = score
		}
	}

	normalized["summary"] = normalizeSummaryText(normalized["summary"])
	return normalized
}

func normalizeKnowledgeGraphResultPayload(raw map[string]interface{}) map[string]interface{} {
	normalized := cloneAnyMap(raw)
	graph := toAnyMap(raw["knowledgeGraph"])

	if _, ok := normalized["nodes"]; !ok {
		if nodes, exists := raw["rfNodes"]; exists {
			normalized["nodes"] = nodes
		} else if nodes, exists := graph["nodes"]; exists {
			normalized["nodes"] = nodes
		} else if rfNodes, exists := graph["rfNodes"]; exists {
			normalized["nodes"] = rfNodes
		}
	}
	if _, ok := normalized["edges"]; !ok {
		if edges, exists := raw["rfEdges"]; exists {
			normalized["edges"] = edges
		} else if edges, exists := graph["edges"]; exists {
			normalized["edges"] = edges
		} else if rfEdges, exists := graph["rfEdges"]; exists {
			normalized["edges"] = rfEdges
		}
	}

	normalized["summary"] = normalizeSummaryText(normalized["summary"])
	if toInt(normalized["entityCount"]) == 0 {
		normalized["entityCount"] = sliceLength(normalized["nodes"])
	}
	if toInt(normalized["relationCount"]) == 0 {
		normalized["relationCount"] = sliceLength(normalized["edges"])
	}

	return normalized
}

func cloneAnyMap(raw map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	if raw == nil {
		return out
	}
	for k, v := range raw {
		out[k] = v
	}
	return out
}

func toAnyMap(value interface{}) map[string]interface{} {
	if value == nil {
		return map[string]interface{}{}
	}
	if m, ok := value.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func sliceLength(value interface{}) int {
	switch t := value.(type) {
	case []interface{}:
		return len(t)
	case []map[string]interface{}:
		return len(t)
	default:
		return 0
	}
}

func normalizeSummaryText(value interface{}) string {
	switch t := value.(type) {
	case string:
		text := strings.TrimSpace(t)
		if text != "" {
			return text
		}
	case map[string]interface{}:
		for _, key := range []string{"text", "summary", "message", "phase"} {
			if candidate, ok := t[key].(string); ok {
				text := strings.TrimSpace(candidate)
				if text != "" {
					return text
				}
			}
		}
	}

	text := strings.TrimSpace(fmt.Sprintf("%v", value))
	if text == "" || text == "<nil>" || text == "map[]" {
		return "生成完成"
	}
	return text
}

func extractResultSummary(taskType string, raw map[string]interface{}) map[string]interface{} {
	summary := normalizeSummaryText(raw["summary"])
	if taskType == constant.AITaskTypeGenerateFaultTree {
		return map[string]interface{}{
			"summary":  summary,
			"accuracy": toIntOrFloat(raw["accuracy"]),
		}
	}
	entityCount := toInt(raw["entityCount"])
	if entityCount == 0 {
		entityCount = toInt(raw["entity_count"])
	}
	if entityCount == 0 {
		entityCount = sliceLength(raw["nodes"])
	}
	relationCount := toInt(raw["relationCount"])
	if relationCount == 0 {
		relationCount = toInt(raw["relation_count"])
	}
	if relationCount == 0 {
		relationCount = sliceLength(raw["edges"])
	}
	return map[string]interface{}{
		"summary":       summary,
		"entityCount":   entityCount,
		"relationCount": relationCount,
	}
}

func toInt(v interface{}) int {
	switch t := v.(type) {
	case int:
		return t
	case int32:
		return int(t)
	case int64:
		return int(t)
	case float64:
		return int(t)
	case float32:
		return int(t)
	case json.Number:
		i, err := t.Int64()
		if err == nil {
			return int(i)
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(t))
		if err == nil {
			return i
		}
	}
	return 0
}

func toIntOrFloat(v interface{}) interface{} {
	switch t := v.(type) {
	case int, int32, int64, float32, float64:
		return t
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return f
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(t), 64); err == nil {
			return f
		}
	}
	return 0
}
