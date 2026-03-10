package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"optitree-backend/internal/ai"
	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/ocr"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type AITaskService struct {
	taskRepo       *repository.AITaskRepository
	docRepo        *repository.DocumentRepository
	projectRepo    *repository.ProjectRepository
	memberRepo     *repository.MemberRepository
	storage        *StorageService
	projectService *ProjectService
	ftService      *FaultTreeService
	kgService      *KnowledgeGraphService
	provider       ai.AIProvider       // used for Chat only
	ocrClient      *ocr.Client         // PaddleOCR layout-parsing
	llmSrvClient   *ai.LLMServerClient // FastAPI LLM generation service
	progressHub    *TaskProgressHub
	rdb            *redis.Client
}

func NewAITaskService(
	taskRepo *repository.AITaskRepository,
	docRepo *repository.DocumentRepository,
	projectRepo *repository.ProjectRepository,
	memberRepo *repository.MemberRepository,
	storage *StorageService,
	projectService *ProjectService,
	ftService *FaultTreeService,
	kgService *KnowledgeGraphService,
	provider ai.AIProvider,
	ocrClient *ocr.Client,
	llmSrvClient *ai.LLMServerClient,
	progressHub *TaskProgressHub,
	rdb *redis.Client,
) *AITaskService {
	return &AITaskService{
		taskRepo:       taskRepo,
		docRepo:        docRepo,
		projectRepo:    projectRepo,
		memberRepo:     memberRepo,
		storage:        storage,
		projectService: projectService,
		ftService:      ftService,
		kgService:      kgService,
		provider:       provider,
		ocrClient:      ocrClient,
		llmSrvClient:   llmSrvClient,
		progressHub:    progressHub,
		rdb:            rdb,
	}
}

// GetTask returns the current state of an AI task.
func (s *AITaskService) GetTask(ctx context.Context, taskID string) (*model.AITask, error) {
	return s.taskRepo.FindByID(taskID)
}

// createTask inserts a new pending task record and caches its initial state in Redis.
func (s *AITaskService) createTask(ctx context.Context, taskType, modelName, userID string, projectID *string) (*model.AITask, error) {
	task := &model.AITask{
		ID:         util.NewAITaskID(),
		Type:       taskType,
		Status:     constant.AITaskStatusPending,
		Progress:   0,
		Stage:      "pending",
		StageLabel: "任务已提交，等待处理",
		Model:      modelName,
		CreatedBy:  userID,
		ProjectID:  projectID,
	}
	if err := s.taskRepo.Create(task); err != nil {
		return nil, err
	}
	s.cacheStatus(ctx, task.ID, task.Status, task.Progress, task.Stage, task.StageLabel)
	return task, nil
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

// extractDocumentTexts resolves docIDs to plain-text / Markdown content for LLM consumption.
//
// Extraction strategy:
//   - txt → read directly from disk (up to 50 KB)
//   - pdf / docx / doc / xlsx / xls → call PaddleOCR layout-parsing API
//   - anything else → placeholder string so the LLM knows the document exists
//
// Each document's text is returned as a separate element in the slice.
// Progress is reported via onProgress(docIndex, totalDocs) after each document.
func (s *AITaskService) extractDocumentTexts(
	docIDs []string,
	onProgress func(done, total int),
) []string {
	if len(docIDs) == 0 {
		return []string{}
	}
	docs, err := s.docRepo.FindByIDs(docIDs)
	if err != nil {
		log.Warn().Err(err).Strs("docIds", docIDs).Msg("查询文档失败")
		return []string{}
	}
	if len(docs) == 0 {
		return []string{}
	}

	total := len(docs)
	contents := make([]string, 0, total)

	for i, doc := range docs {
		localPath := s.storage.LocalPath(doc.SourceURL)
		if localPath == "" {
			contents = append(contents, fmt.Sprintf("[Document: %s — storage path unavailable]", doc.FileName))
			if onProgress != nil {
				onProgress(i+1, total)
			}
			continue
		}

		switch {
		case doc.FileType == "txt":
			text, err := readTextFile(localPath, 50_000)
			if err == nil {
				contents = append(contents, text)
			} else {
				log.Warn().Err(err).Str("docId", doc.ID).Msg("读取 txt 文档失败")
				contents = append(contents, fmt.Sprintf("[Document: %s — read error]", doc.FileName))
			}

		case ocr.IsBinaryDoc(doc.FileType):
			// PDF, DOCX, XLSX etc. — use PaddleOCR.
			// PaddleOCR API only accepts fileType=0 (PDF) or fileType=1 (image).
			// Non-PDF binary formats fall back to a placeholder until a dedicated
			// extraction path (e.g. docx XML parser) is added (see project notes).
			if doc.FileType != "pdf" {
				contents = append(contents, fmt.Sprintf(
					"[Document: %s (%s) — non-PDF binary; text extraction not yet supported]",
					doc.FileName, doc.FileType))
				break
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			markdown, err := s.ocrClient.ParseToMarkdown(ctx, localPath, ocr.FileTypePDF)
			cancel()
			if err != nil {
				log.Warn().Err(err).Str("docId", doc.ID).Msg("OCR 解析失败，使用占位文本")
				contents = append(contents, fmt.Sprintf(
					"[Document: %s (pdf) — OCR failed: %s]", doc.FileName, err.Error()))
			} else if markdown == "" {
				contents = append(contents, fmt.Sprintf("[Document: %s (pdf) — OCR returned empty]", doc.FileName))
			} else {
				contents = append(contents, markdown)
			}

		default:
			contents = append(contents, fmt.Sprintf(
				"[Document: %s (%s) — unsupported format]", doc.FileName, doc.FileType))
		}

		if onProgress != nil {
			onProgress(i+1, total)
		}
	}
	return contents
}

func readTextFile(path string, maxBytes int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	r := io.LimitReader(f, maxBytes)
	scanner := bufio.NewScanner(r)
	var sb strings.Builder
	for scanner.Scan() {
		sb.WriteString(scanner.Text())
		sb.WriteByte('\n')
	}
	return sb.String(), scanner.Err()
}

// ─── Generate Fault Tree ──────────────────────────────────────────────────────

// GenerateFaultTreeInput is the service-layer input for fault tree generation.
type GenerateFaultTreeInput struct {
	DocIDs    []string
	TopEvent  string
	Config    ai.GenerateConfig
	ProjectID *string
	UserID    string
}

// GenerateFaultTreeOutput is the immediate API response — a task reference for polling.
type GenerateFaultTreeOutput struct {
	TaskID    string `json:"taskId"`
	Status    string `json:"status"`
	ProjectID string `json:"projectId"`
}

// GenerateFaultTree creates an async task and launches generation in a goroutine.
// The caller polls GET /ai/tasks/:taskId for progress and the final result.
func (s *AITaskService) GenerateFaultTree(ctx context.Context, input GenerateFaultTreeInput) (*GenerateFaultTreeOutput, error) {
	project, err := s.resolveOrCreateProject(ctx, input.ProjectID, input.UserID, constant.ProjectTypeFT, input.TopEvent)
	if err != nil {
		return nil, err
	}
	if err := s.setProjectGenerationStatus(project.ID, constant.ProjectGenerationPending); err != nil {
		return nil, err
	}

	modelName := input.Config.Model
	if modelName == "" {
		modelName = "default"
	}
	projectID := project.ID
	task, err := s.createTask(ctx, constant.AITaskTypeGenerateFaultTree, modelName, input.UserID, &projectID)
	if err != nil {
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
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

	go s.runGenerateFaultTree(task.ID, projectID, input)
	return &GenerateFaultTreeOutput{TaskID: task.ID, Status: task.Status, ProjectID: projectID}, nil
}

func (s *AITaskService) runGenerateFaultTree(taskID, projectID string, input GenerateFaultTreeInput) {
	bg := context.Background()
	if err := s.setProjectGenerationStatus(projectID, constant.ProjectGenerationRunning); err != nil {
		log.Error().Err(err).Str("projectId", projectID).Msg("更新项目生成状态失败")
	}

	// ─ Stage 1: document parsing (10% → 40%) ───────────────────────────────
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusGenerating,
		ProjectStatus: constant.ProjectGenerationRunning,
		Progress:      10,
		Stage:         "parsing",
		StageLabel:    "正在解析文档",
	})

	contents := s.extractDocumentTexts(input.DocIDs, func(done, total int) {
		// Map per-document completion to 10-40% range.
		pct := 10 + int(float64(done)/float64(total)*30)
		label := fmt.Sprintf("正在解析文档 (%d/%d)", done, total)
		// Only update Redis during this phase to avoid hammering the DB.
		s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, pct, "parsing", label)
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.progress",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusGenerating,
			ProjectStatus: constant.ProjectGenerationRunning,
			Progress:      pct,
			Stage:         "parsing",
			StageLabel:    label,
		})
	})
	if len(contents) == 0 {
		errMsg := "没有可用于 AI 生成的文档内容"
		log.Warn().Str("taskId", taskID).Strs("docIds", input.DocIDs).Msg(errMsg)
		_ = s.taskRepo.SetFailed(taskID, errMsg)
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
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
		return
	}

	// ─ Stage 2: LLM generation (40% → 90%) ──────────────────────────────
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusGenerating,
		ProjectStatus: constant.ProjectGenerationRunning,
		Progress:      40,
		Stage:         "generating",
		StageLabel:    "AI 生成中",
	})

	ctx, cancel := context.WithTimeout(bg, 5*time.Minute)
	defer cancel()

	// Forward SSE progress events from the LLM server to Redis (no DB write in this phase).
	onProgress := func(stage string, pct int) {
		// Clamp the LLM server's 0-100 pct to our 40-90% window.
		mapped := 40 + int(float64(pct)*0.5)
		if mapped > 90 {
			mapped = 90
		}
		if mapped < 40 {
			mapped = 40
		}
		s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, mapped, stage, "AI 生成中")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.progress",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusGenerating,
			ProjectStatus: constant.ProjectGenerationRunning,
			Progress:      mapped,
			Stage:         stage,
			StageLabel:    "AI 生成中",
		})
	}

	result, err := s.llmSrvClient.GenerateFaultTree(ctx, contents, input.TopEvent, input.Config, onProgress)
	if err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("AI 故障树生成失败")
		_ = s.taskRepo.SetFailed(taskID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.failed",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusFailed,
			ProjectStatus: constant.ProjectGenerationFailed,
			ErrorMessage:  err.Error(),
			Stage:         "failed",
			StageLabel:    "生成失败",
		})
		return
	}

	if err := s.saveGeneratedFaultTreeToProject(ctx, projectID, result); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Str("projectId", projectID).Msg("保存故障树图数据失败")
		_ = s.taskRepo.SetFailed(taskID, "保存项目图数据失败: "+err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.failed",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusFailed,
			ProjectStatus: constant.ProjectGenerationFailed,
			ErrorMessage:  "保存项目图数据失败: " + err.Error(),
			Stage:         "failed",
			StageLabel:    "生成失败",
		})
		return
	}

	// ─ Stage 3: persist result (90% → 100%) ─────────────────────────────
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 90, "saving", "正在保存结果")
	resultJSON, _ := json.Marshal(result)
	if err := s.taskRepo.SetCompleted(taskID, resultJSON); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("保存 AI 任务结果失败")
	}
	_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationCompleted)
	s.cacheStatus(bg, taskID, constant.AITaskStatusCompleted, 100, "completed", "生成完成")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.completed",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusCompleted,
		ProjectStatus: constant.ProjectGenerationCompleted,
		Progress:      100,
		Stage:         "completed",
		StageLabel:    "生成完成",
		Result: map[string]interface{}{
			"summary":  result.Summary,
			"accuracy": result.Accuracy,
		},
	})
}

// ─── Generate Knowledge Graph ─────────────────────────────────────────────────

// GenerateKnowledgeGraphInput is the service-layer input for knowledge graph generation.
type GenerateKnowledgeGraphInput struct {
	DocIDs    []string
	Config    ai.GenerateConfig
	ProjectID *string
	UserID    string
}

// GenerateKnowledgeGraphOutput is the immediate API response — a task reference for polling.
type GenerateKnowledgeGraphOutput struct {
	TaskID    string `json:"taskId"`
	Status    string `json:"status"`
	ProjectID string `json:"projectId"`
}

// GenerateKnowledgeGraph creates an async task and launches generation in a goroutine.
func (s *AITaskService) GenerateKnowledgeGraph(ctx context.Context, input GenerateKnowledgeGraphInput) (*GenerateKnowledgeGraphOutput, error) {
	project, err := s.resolveOrCreateProject(ctx, input.ProjectID, input.UserID, constant.ProjectTypeKG, "")
	if err != nil {
		return nil, err
	}
	if err := s.setProjectGenerationStatus(project.ID, constant.ProjectGenerationPending); err != nil {
		return nil, err
	}

	modelName := input.Config.Model
	if modelName == "" {
		modelName = "default"
	}
	projectID := project.ID
	task, err := s.createTask(ctx, constant.AITaskTypeGenerateKnowledgeGraph, modelName, input.UserID, &projectID)
	if err != nil {
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
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

	go s.runGenerateKnowledgeGraph(task.ID, projectID, input)
	return &GenerateKnowledgeGraphOutput{TaskID: task.ID, Status: task.Status, ProjectID: projectID}, nil
}

func (s *AITaskService) runGenerateKnowledgeGraph(taskID, projectID string, input GenerateKnowledgeGraphInput) {
	bg := context.Background()
	if err := s.setProjectGenerationStatus(projectID, constant.ProjectGenerationRunning); err != nil {
		log.Error().Err(err).Str("projectId", projectID).Msg("更新项目生成状态失败")
	}

	// ─ Stage 1: document parsing (10% → 40%) ───────────────────────────────
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusGenerating,
		ProjectStatus: constant.ProjectGenerationRunning,
		Progress:      10,
		Stage:         "parsing",
		StageLabel:    "正在解析文档",
	})

	contents := s.extractDocumentTexts(input.DocIDs, func(done, total int) {
		pct := 10 + int(float64(done)/float64(total)*30)
		label := fmt.Sprintf("正在解析文档 (%d/%d)", done, total)
		s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, pct, "parsing", label)
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.progress",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusGenerating,
			ProjectStatus: constant.ProjectGenerationRunning,
			Progress:      pct,
			Stage:         "parsing",
			StageLabel:    label,
		})
	})
	if len(contents) == 0 {
		errMsg := "没有可用于 AI 生成的文档内容"
		log.Warn().Str("taskId", taskID).Strs("docIds", input.DocIDs).Msg(errMsg)
		_ = s.taskRepo.SetFailed(taskID, errMsg)
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
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
		return
	}

	// ─ Stage 2: LLM generation (40% → 90%) ──────────────────────────────
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusGenerating,
		ProjectStatus: constant.ProjectGenerationRunning,
		Progress:      40,
		Stage:         "generating",
		StageLabel:    "AI 生成中",
	})

	ctx, cancel := context.WithTimeout(bg, 5*time.Minute)
	defer cancel()

	onProgress := func(stage string, pct int) {
		mapped := 40 + int(float64(pct)*0.5)
		if mapped > 90 {
			mapped = 90
		}
		if mapped < 40 {
			mapped = 40
		}
		s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, mapped, stage, "AI 生成中")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.progress",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusGenerating,
			ProjectStatus: constant.ProjectGenerationRunning,
			Progress:      mapped,
			Stage:         stage,
			StageLabel:    "AI 生成中",
		})
	}

	result, err := s.llmSrvClient.GenerateKnowledgeGraph(ctx, contents, input.Config, onProgress)
	if err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("AI 知识图谱生成失败")
		_ = s.taskRepo.SetFailed(taskID, err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.failed",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusFailed,
			ProjectStatus: constant.ProjectGenerationFailed,
			ErrorMessage:  err.Error(),
			Stage:         "failed",
			StageLabel:    "生成失败",
		})
		return
	}

	if err := s.saveGeneratedKnowledgeGraphToProject(ctx, projectID, result); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Str("projectId", projectID).Msg("保存知识图谱图数据失败")
		_ = s.taskRepo.SetFailed(taskID, "保存项目图数据失败: "+err.Error())
		_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationFailed)
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		s.publishTaskEvent(TaskProgressEvent{
			Event:         "task.failed",
			ProjectID:     projectID,
			TaskID:        taskID,
			Status:        constant.AITaskStatusFailed,
			ProjectStatus: constant.ProjectGenerationFailed,
			ErrorMessage:  "保存项目图数据失败: " + err.Error(),
			Stage:         "failed",
			StageLabel:    "生成失败",
		})
		return
	}

	// ─ Stage 3: persist result (90% → 100%) ─────────────────────────────
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 90, "saving", "正在保存结果")
	resultJSON, _ := json.Marshal(result)
	if err := s.taskRepo.SetCompleted(taskID, resultJSON); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("保存 AI 任务结果失败")
	}
	_ = s.setProjectGenerationStatus(projectID, constant.ProjectGenerationCompleted)
	s.cacheStatus(bg, taskID, constant.AITaskStatusCompleted, 100, "completed", "生成完成")
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.completed",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusCompleted,
		ProjectStatus: constant.ProjectGenerationCompleted,
		Progress:      100,
		Stage:         "completed",
		StageLabel:    "生成完成",
		Result: map[string]interface{}{
			"summary":       result.Summary,
			"entityCount":   result.EntityCount,
			"relationCount": result.RelationCount,
		},
	})
}

// ─── Chat ─────────────────────────────────────────────────────────────────────

// ChatInput is the service-layer input for AI chat.
type ChatInput struct {
	ContextData interface{}
	GraphType   string
	Message     string
	Model       string
}

// Chat performs synchronous AI conversation about a graph.
func (s *AITaskService) Chat(ctx context.Context, input ChatInput) (*ai.ChatResponse, error) {
	return s.provider.Chat(ctx, ai.ChatRequest{
		ContextData: input.ContextData,
		GraphType:   input.GraphType,
		Message:     input.Message,
		Model:       input.Model,
	})
}

// ChatStreamInput is the service-layer input for streaming AI chat.
type ChatStreamInput struct {
	ContextData interface{}
	ContextType string // "faultTree" | "knowledgeGraph"
	Message     string
}

// ChatStream streams AI replies token by token via onChunk.
// Returns the total tokens consumed and the model name actually used.
func (s *AITaskService) ChatStream(ctx context.Context, input ChatStreamInput, onChunk func(chunk string)) (tokensUsed int, modelUsed string, err error) {
	return s.provider.ChatStream(ctx, ai.ChatRequest{
		ContextData: input.ContextData,
		GraphType:   input.ContextType,
		Message:     input.Message,
	}, onChunk)
}
