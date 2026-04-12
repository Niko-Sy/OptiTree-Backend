package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"optitree-backend/internal/constant"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

func (s *AITaskService) StartFaultTreeDispatcher(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	s.dispatcherMu.Lock()
	defer s.dispatcherMu.Unlock()
	if s.dispatcherStarted {
		return nil
	}

	if err := s.ensureProducerGroup(ctx); err != nil {
		return err
	}

	dispatcherCtx, cancel := context.WithCancel(ctx)
	s.dispatcherCtx = dispatcherCtx
	s.dispatcherCancel = cancel
	s.dispatcherStarted = true

	for idx := 0; idx < s.dispatcherWorkers; idx++ {
		consumer := fmt.Sprintf("ft-dispatcher-%d", idx+1)
		s.dispatcherWG.Add(1)
		go func(name string) {
			defer s.dispatcherWG.Done()
			s.dispatcherLoop(dispatcherCtx, name)
		}(consumer)
	}

	log.Info().
		Str("producerStream", s.producerStream).
		Str("producerGroup", s.producerGroup).
		Int("workers", s.dispatcherWorkers).
		Msg("AI fault-tree dispatcher started")
	return nil
}

func (s *AITaskService) StopFaultTreeDispatcher() {
	s.dispatcherMu.Lock()
	if !s.dispatcherStarted {
		s.dispatcherMu.Unlock()
		return
	}
	cancel := s.dispatcherCancel
	s.dispatcherMu.Unlock()

	if cancel != nil {
		cancel()
	}
	s.dispatcherWG.Wait()

	s.dispatcherMu.Lock()
	s.dispatcherStarted = false
	s.dispatcherCancel = nil
	s.dispatcherCtx = nil
	s.dispatcherMu.Unlock()
}

func (s *AITaskService) ensureProducerGroup(ctx context.Context) error {
	err := s.rdb.XGroupCreateMkStream(ctx, s.producerStream, s.producerGroup, "0").Err()
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return fmt.Errorf("create producer group failed: %w", err)
}

func (s *AITaskService) dispatcherLoop(ctx context.Context, consumer string) {
	for {
		if ctx.Err() != nil {
			return
		}

		if err := s.releaseDelayedProducerTasks(ctx); err != nil {
			log.Warn().Err(err).Str("consumer", consumer).Msg("release delayed producer tasks failed")
		}

		entries, err := s.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    s.producerGroup,
			Consumer: consumer,
			Streams:  []string{s.producerStream, ">"},
			Count:    s.producerReadCount,
			Block:    time.Duration(s.producerBlockMS) * time.Millisecond,
		}).Result()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return
			}
			if err == redis.Nil {
				continue
			}
			if strings.Contains(err.Error(), "NOGROUP") {
				if groupErr := s.ensureProducerGroup(ctx); groupErr != nil {
					log.Warn().Err(groupErr).Msg("recreate producer group failed")
				}
				continue
			}
			log.Warn().Err(err).Str("consumer", consumer).Msg("producer xreadgroup failed")
			time.Sleep(500 * time.Millisecond)
			continue
		}

		for _, stream := range entries {
			for _, message := range stream.Messages {
				s.handleProducerMessage(ctx, stream.Stream, message)
			}
		}
	}
}

func (s *AITaskService) handleProducerMessage(ctx context.Context, stream string, message redis.XMessage) {
	payloadText := strings.TrimSpace(fmt.Sprintf("%v", message.Values["payload"]))
	if payloadText == "" {
		_ = s.rdb.XAck(ctx, stream, s.producerGroup, message.ID).Err()
		return
	}

	var payload AITaskQueueMessage
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		log.Warn().Err(err).Str("messageId", message.ID).Msg("invalid producer payload")
		_ = s.rdb.XAck(ctx, stream, s.producerGroup, message.ID).Err()
		return
	}

	err := s.dispatchFaultTreePayload(ctx, payload, payloadText)
	if err != nil {
		log.Warn().
			Err(err).
			Str("taskId", payload.TaskID).
			Str("projectId", payload.ProjectID).
			Msg("dispatch fault-tree payload failed")
		_ = s.requeueDelayedProducerPayload(ctx, payloadText, s.producerRetryDelay)
	}

	_ = s.rdb.XAck(ctx, stream, s.producerGroup, message.ID).Err()
}

func (s *AITaskService) dispatchFaultTreePayload(ctx context.Context, payload AITaskQueueMessage, payloadText string) error {
	if strings.TrimSpace(payload.TaskType) != constant.AITaskTypeGenerateFaultTree {
		return s.enqueueTask(ctx, payload)
	}

	taskID := strings.TrimSpace(payload.TaskID)
	projectID := strings.TrimSpace(payload.ProjectID)
	if taskID == "" || projectID == "" {
		return errors.New("taskId or projectId is empty")
	}

	s.updateDispatchState(ctx, payload, 4, "dispatching", "任务正在等待执行槽位")

	locked, err := s.tryAcquireProjectLock(ctx, projectID, taskID)
	if err != nil {
		return err
	}
	if !locked {
		s.updateDispatchState(ctx, payload, 3, "waiting_project_slot", "同项目任务执行中，已加入等待队列")
		return s.requeueDelayedProducerPayload(ctx, payloadText, s.producerRetryDelay)
	}

	if err := s.enqueueTask(ctx, payload); err != nil {
		s.releaseProjectLock(ctx, projectID, taskID)
		s.updateDispatchState(ctx, payload, 3, "dispatch_retrying", "投递 Worker 队列失败，稍后重试")
		return err
	}

	s.updateDispatchState(ctx, payload, 8, "enqueued", "任务已投递至 Worker")
	return nil
}

func (s *AITaskService) updateDispatchState(
	ctx context.Context,
	payload AITaskQueueMessage,
	progress int,
	stage string,
	stageLabel string,
) {
	taskID := strings.TrimSpace(payload.TaskID)
	projectID := strings.TrimSpace(payload.ProjectID)
	if taskID == "" || projectID == "" {
		return
	}

	_ = s.taskRepo.UpdateStatus(taskID, constant.AITaskStatusPending, progress, stage, stageLabel)
	s.cacheStatus(ctx, taskID, constant.AITaskStatusPending, progress, stage, stageLabel)
	s.publishTaskEvent(TaskProgressEvent{
		Event:         "task.progress",
		ProjectID:     projectID,
		TaskID:        taskID,
		Status:        constant.AITaskStatusPending,
		ProjectStatus: constant.ProjectGenerationPending,
		Progress:      progress,
		Stage:         stage,
		StageLabel:    stageLabel,
	})
}

func (s *AITaskService) releaseDelayedProducerTasks(ctx context.Context) error {
	nowMs := time.Now().UnixMilli()
	payloads, err := s.rdb.ZRangeByScore(ctx, s.producerDelayedZSet, &redis.ZRangeBy{
		Min:    "-inf",
		Max:    strconv.FormatInt(nowMs, 10),
		Offset: 0,
		Count:  s.producerReadCount,
	}).Result()
	if err != nil {
		return err
	}
	if len(payloads) == 0 {
		return nil
	}

	for _, payloadText := range payloads {
		removed, remErr := s.rdb.ZRem(ctx, s.producerDelayedZSet, payloadText).Result()
		if remErr != nil || removed == 0 {
			continue
		}
		entryID, addErr := s.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: s.producerStream,
			MaxLen: s.queueMaxLen,
			Approx: true,
			Values: map[string]interface{}{
				"payload": payloadText,
			},
		}).Result()
		if addErr != nil {
			continue
		}

		taskID := parseQueueMessageTaskID(payloadText)
		if taskID != "" {
			s.recordTaskStreamEntry(ctx, taskID, aiTaskStreamSourceProducer, entryID)
		}
	}
	return nil
}

func (s *AITaskService) requeueDelayedProducerPayload(ctx context.Context, payloadText string, delayMS int64) error {
	if strings.TrimSpace(payloadText) == "" {
		return nil
	}
	if delayMS <= 0 {
		entryID, err := s.rdb.XAdd(ctx, &redis.XAddArgs{
			Stream: s.producerStream,
			MaxLen: s.queueMaxLen,
			Approx: true,
			Values: map[string]interface{}{
				"payload": payloadText,
			},
		}).Result()
		if err != nil {
			return err
		}

		taskID := parseQueueMessageTaskID(payloadText)
		if taskID != "" {
			s.recordTaskStreamEntry(ctx, taskID, aiTaskStreamSourceProducer, entryID)
		}
		return nil
	}
	retryAt := time.Now().UnixMilli() + delayMS
	return s.rdb.ZAdd(ctx, s.producerDelayedZSet, redis.Z{
		Score:  float64(retryAt),
		Member: payloadText,
	}).Err()
}

func parseQueueMessageTaskID(payloadText string) string {
	if strings.TrimSpace(payloadText) == "" {
		return ""
	}
	var payload AITaskQueueMessage
	if err := json.Unmarshal([]byte(payloadText), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.TaskID)
}

func (s *AITaskService) tryAcquireProjectLock(ctx context.Context, projectID, taskID string) (bool, error) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" {
		return false, nil
	}
	key := constant.RedisKeyAITaskLock + projectID
	return s.rdb.SetNX(ctx, key, taskID, s.projectLockTTL).Result()
}

func (s *AITaskService) touchProjectLock(ctx context.Context, projectID, taskID string) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" {
		return
	}
	key := constant.RedisKeyAITaskLock + projectID
	lockTaskID, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		if err == redis.Nil {
			_, _ = s.rdb.SetNX(ctx, key, taskID, s.projectLockTTL).Result()
		}
		return
	}
	if lockTaskID != taskID {
		return
	}
	_ = s.rdb.Expire(ctx, key, s.projectLockTTL).Err()
}

func (s *AITaskService) releaseProjectLock(ctx context.Context, projectID, taskID string) {
	if strings.TrimSpace(projectID) == "" || strings.TrimSpace(taskID) == "" {
		return
	}
	key := constant.RedisKeyAITaskLock + projectID
	lockTaskID, err := s.rdb.Get(ctx, key).Result()
	if err != nil {
		return
	}
	if lockTaskID != taskID {
		return
	}
	_ = s.rdb.Del(ctx, key).Err()
}
