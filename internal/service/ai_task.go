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
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type AITaskService struct {
	taskRepo *repository.AITaskRepository
	docRepo  *repository.DocumentRepository
	storage  *StorageService
	provider ai.AIProvider
	rdb      *redis.Client
}

func NewAITaskService(
	taskRepo *repository.AITaskRepository,
	docRepo *repository.DocumentRepository,
	storage *StorageService,
	provider ai.AIProvider,
	rdb *redis.Client,
) *AITaskService {
	return &AITaskService{
		taskRepo: taskRepo,
		docRepo:  docRepo,
		storage:  storage,
		provider: provider,
		rdb:      rdb,
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

// readDocumentContents resolves docIDs to text content for LLM consumption.
// For binary formats (PDF, DOCX) a placeholder is included so the LLM is aware
// documents exist even without full text extraction support.
func (s *AITaskService) readDocumentContents(docIDs []string) []string {
	if len(docIDs) == 0 {
		return nil
	}
	docs, err := s.docRepo.FindByIDs(docIDs)
	if err != nil || len(docs) == 0 {
		return nil
	}
	contents := make([]string, 0, len(docs))
	for _, doc := range docs {
		localPath := s.storage.LocalPath(doc.SourceURL)
		if localPath == "" {
			contents = append(contents, fmt.Sprintf("[Document: %s — storage path unavailable]", doc.FileName))
			continue
		}
		if doc.FileType == "txt" {
			text, err := readTextFile(localPath, 50_000)
			if err == nil {
				contents = append(contents, text)
				continue
			}
		}
		// Binary formats: include a placeholder until a text-extraction pipeline is added.
		contents = append(contents, fmt.Sprintf("[Document: %s (%s) — binary format, text extraction pending]",
			doc.FileName, doc.FileType))
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
	DocIDs   []string
	TopEvent string
	Config   ai.GenerateConfig
	UserID   string
}

// GenerateFaultTreeOutput is the immediate API response — a task reference for polling.
type GenerateFaultTreeOutput struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

// GenerateFaultTree creates an async task and launches generation in a goroutine.
// The caller polls GET /ai/tasks/:taskId for progress and the final result.
func (s *AITaskService) GenerateFaultTree(ctx context.Context, input GenerateFaultTreeInput) (*GenerateFaultTreeOutput, error) {
	modelName := input.Config.Model
	if modelName == "" {
		modelName = "default"
	}
	task, err := s.createTask(ctx, constant.AITaskTypeGenerateFaultTree, modelName, input.UserID, nil)
	if err != nil {
		return nil, err
	}
	go s.runGenerateFaultTree(task.ID, input)
	return &GenerateFaultTreeOutput{TaskID: task.ID, Status: task.Status}, nil
}

func (s *AITaskService) runGenerateFaultTree(taskID string, input GenerateFaultTreeInput) {
	bg := context.Background()
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")

	contents := s.readDocumentContents(input.DocIDs)

	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")

	ctx, cancel := context.WithTimeout(bg, 5*time.Minute)
	defer cancel()

	result, err := s.provider.GenerateFaultTree(ctx, ai.GenerateFaultTreeRequest{
		DocumentContents: contents,
		TopEvent:         input.TopEvent,
		Config:           input.Config,
	})
	if err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("AI 故障树生成失败")
		_ = s.taskRepo.SetFailed(taskID, err.Error())
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return
	}

	resultJSON, _ := json.Marshal(result)
	if err := s.taskRepo.SetCompleted(taskID, resultJSON); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("保存 AI 任务结果失败")
	}
	s.cacheStatus(bg, taskID, constant.AITaskStatusCompleted, 100, "completed", "生成完成")
}

// ─── Generate Knowledge Graph ─────────────────────────────────────────────────

// GenerateKnowledgeGraphInput is the service-layer input for knowledge graph generation.
type GenerateKnowledgeGraphInput struct {
	DocIDs []string
	Config ai.GenerateConfig
	UserID string
}

// GenerateKnowledgeGraphOutput is the immediate API response — a task reference for polling.
type GenerateKnowledgeGraphOutput struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

// GenerateKnowledgeGraph creates an async task and launches generation in a goroutine.
func (s *AITaskService) GenerateKnowledgeGraph(ctx context.Context, input GenerateKnowledgeGraphInput) (*GenerateKnowledgeGraphOutput, error) {
	modelName := input.Config.Model
	if modelName == "" {
		modelName = "default"
	}
	task, err := s.createTask(ctx, constant.AITaskTypeGenerateKnowledgeGraph, modelName, input.UserID, nil)
	if err != nil {
		return nil, err
	}
	go s.runGenerateKnowledgeGraph(task.ID, input)
	return &GenerateKnowledgeGraphOutput{TaskID: task.ID, Status: task.Status}, nil
}

func (s *AITaskService) runGenerateKnowledgeGraph(taskID string, input GenerateKnowledgeGraphInput) {
	bg := context.Background()
	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 10, "parsing", "正在解析文档")

	contents := s.readDocumentContents(input.DocIDs)

	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")
	s.cacheStatus(bg, taskID, constant.AITaskStatusGenerating, 40, "generating", "AI 生成中")

	ctx, cancel := context.WithTimeout(bg, 5*time.Minute)
	defer cancel()

	result, err := s.provider.GenerateKnowledgeGraph(ctx, ai.GenerateKnowledgeGraphRequest{
		DocumentContents: contents,
		Config:           input.Config,
	})
	if err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("AI 知识图谱生成失败")
		_ = s.taskRepo.SetFailed(taskID, err.Error())
		s.cacheStatus(bg, taskID, constant.AITaskStatusFailed, 0, "failed", "生成失败")
		return
	}

	resultJSON, _ := json.Marshal(result)
	if err := s.taskRepo.SetCompleted(taskID, resultJSON); err != nil {
		log.Error().Err(err).Str("taskId", taskID).Msg("保存 AI 任务结果失败")
	}
	s.cacheStatus(bg, taskID, constant.AITaskStatusCompleted, 100, "completed", "生成完成")
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
