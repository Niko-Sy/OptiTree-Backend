package service

import (
	"context"

	"optitree-backend/internal/constant"
	"optitree-backend/internal/model"
	"optitree-backend/internal/repository"
	"optitree-backend/internal/util"

	"github.com/redis/go-redis/v9"
)

type AITaskService struct {
	taskRepo *repository.AITaskRepository
	rdb      *redis.Client
}

func NewAITaskService(taskRepo *repository.AITaskRepository, rdb *redis.Client) *AITaskService {
	return &AITaskService{taskRepo: taskRepo, rdb: rdb}
}

// GetTask 获取 AI 任务状态（P0 仅提供查询）
func (s *AITaskService) GetTask(ctx context.Context, taskID string) (*model.AITask, error) {
	return s.taskRepo.FindByID(taskID)
}

// CreateTask 创建 AI 任务占位记录（供 P1 AI 接口使用）
func (s *AITaskService) CreateTask(ctx context.Context, taskType, modelName, userID string, projectID *string) (*model.AITask, error) {
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
	key := constant.RedisKeyAITask + task.ID
	_ = s.rdb.HSet(ctx, key,
		"status", task.Status,
		"progress", task.Progress,
		"stage", task.Stage,
		"stageLabel", task.StageLabel,
	).Err()
	return task, nil
}
